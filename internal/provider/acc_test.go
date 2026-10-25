package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"routeur": providerserver.NewProtocol6WithError(New("test")()),
}

// testAccPreCheck fails fast unless the acceptance test environment is set,
// and refuses to run against production.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	for _, env := range []string{EnvBaseURL, EnvToken, EnvOrg} {
		if os.Getenv(env) == "" {
			t.Fatalf("%s must be set for acceptance tests", env)
		}
	}
	if strings.Contains(strings.ToLower(os.Getenv(EnvBaseURL)), "app.routeur.ai") {
		t.Fatalf("%s=%s: acceptance tests create and delete real objects and do not run against production; point it at a non-production environment", EnvBaseURL, os.Getenv(EnvBaseURL))
	}
}

// testAccClient is a client for out-of-band setup and CheckDestroy.
func testAccClient(t *testing.T) (*client.Client, string) {
	t.Helper()
	c, err := client.New(client.Config{BaseURL: os.Getenv(EnvBaseURL), Token: os.Getenv(EnvToken)})
	if err != nil {
		t.Fatal(err)
	}
	return c, os.Getenv(EnvOrg)
}

// testAccSlug is a unique hyphen slug, e.g. "tfacc-ws-k3j9x2".
func testAccSlug(prefix string) string {
	return "tfacc-" + prefix + "-" + acctest.RandStringFromCharSet(6, "abcdefghijklmnopqrstuvwxyz0123456789")
}

// testAccCheckGone is a CheckDestroy: every resource of typeName must 404.
// pathFor builds its API path from the state attributes.
func testAccCheckGone(t *testing.T, typeName string, pathFor func(org string, attrs map[string]string) string) func(*terraform.State) error {
	return func(s *terraform.State) error {
		c, org := testAccClient(t)
		for _, rs := range s.RootModule().Resources {
			if rs.Type != typeName {
				continue
			}
			p := pathFor(org, rs.Primary.Attributes)
			err := c.Do(context.Background(), http.MethodGet, p, nil, nil)
			if err == nil {
				return fmt.Errorf("%s %s still exists after destroy", typeName, rs.Primary.ID)
			}
			if !client.IsNotFound(err) {
				return fmt.Errorf("checking %s %s: %w", typeName, rs.Primary.ID, err)
			}
		}
		return nil
	}
}

// testAccImportID returns `<workspace_id>/<id>` for a workspace-nested
// resource, for ImportStateIdFunc.
func testAccImportID(resourceName string) func(*terraform.State) (string, error) {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return "", fmt.Errorf("%s not in state", resourceName)
		}
		return rs.Primary.Attributes["workspace_id"] + "/" + rs.Primary.ID, nil
	}
}

// An organisation has one credential per provider, so every out-of-band
// credential write takes that provider's lock.
//
// So the suite passes in one organisation in any order:
//   - testAccSharedCredentialProvider (openai) is shared: tests that only
//     need its models call testAccEnsureCredential and never delete it.
//   - Tests of routeur_provider_credential itself use the other providers,
//     each claiming its provider with testAccFreeProvider first.
const testAccSharedCredentialProvider = "openai"

var testAccCredentialLocks sync.Map // provider slug → *sync.Mutex

// testAccSkipUnlessAcc skips a test that does out-of-band setup before
// resource.Test (which would skip it itself) when TF_ACC is not set.
func testAccSkipUnlessAcc(t *testing.T) {
	t.Helper()
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skipf("acceptance test: set %s=1", resource.EnvTfAcc)
	}
	testAccPreCheck(t)
}

func testAccCredentialLock(provider string) *sync.Mutex {
	m, _ := testAccCredentialLocks.LoadOrStore(provider, &sync.Mutex{})
	mu, _ := m.(*sync.Mutex) // only ever stores *sync.Mutex
	return mu
}

// testAccListCredentials returns the organisation's credentials for provider.
func testAccListCredentials(t *testing.T, provider string) []apiProviderCredential {
	t.Helper()
	c, org := testAccClient(t)
	all, err := client.ListAll[apiProviderCredential](context.Background(), c, client.Path("orgs", org, "provider-credentials"))
	if err != nil {
		t.Fatalf("listing provider credentials: %v", err)
	}
	var out []apiProviderCredential
	for _, cr := range all {
		if cr.Provider == provider {
			out = append(out, cr)
		}
	}
	return out
}

// testAccEnsureCredential makes sure the organisation has an enabled
// credential for the shared provider, so its models are listed: it reuses
// (and enables) an existing one, else creates one. Idempotent; the
// credential is left in place for other tests.
//
//nolint:unparam // provider names, at each call site, which credential the test relies on
func testAccEnsureCredential(t *testing.T, provider string) {
	t.Helper()
	if provider != testAccSharedCredentialProvider {
		t.Fatalf("testAccEnsureCredential(%q): only %q is shared between tests", provider, testAccSharedCredentialProvider)
	}
	testAccSkipUnlessAcc(t)
	mu := testAccCredentialLock(provider)
	mu.Lock()
	defer mu.Unlock()

	ctx := context.Background()
	c, org := testAccClient(t)
	if existing := testAccListCredentials(t, provider); len(existing) > 0 {
		if !existing[0].Enabled {
			if err := c.Do(ctx, http.MethodPatch, client.Path("orgs", org, "provider-credentials", existing[0].ID), map[string]any{"enabled": true}, nil); err != nil {
				t.Fatalf("enabling the %s credential: %v", provider, err)
			}
		}
		return
	}
	err := c.Do(ctx, http.MethodPost, client.Path("orgs", org, "provider-credentials"),
		map[string]any{"provider": provider, "label": "acceptance", "key": "sk-acceptance-" + provider + "-key"}, nil)
	if err != nil {
		t.Fatalf("creating the %s credential: %v", provider, err)
	}
}

// testAccFreeProvider claims provider for a routeur_provider_credential
// test: it deletes any credential left for it (by an earlier, interrupted
// run in a reused organisation) and holds the provider's lock until the
// test ends.
func testAccFreeProvider(t *testing.T, provider string) {
	t.Helper()
	if provider == testAccSharedCredentialProvider {
		t.Fatalf("testAccFreeProvider(%q): that provider is shared; use another", provider)
	}
	testAccSkipUnlessAcc(t)
	mu := testAccCredentialLock(provider)
	mu.Lock()
	t.Cleanup(mu.Unlock)

	c, org := testAccClient(t)
	for _, cr := range testAccListCredentials(t, provider) {
		if err := c.Do(context.Background(), http.MethodDelete, client.Path("orgs", org, "provider-credentials", cr.ID), nil, nil); err != nil && !client.IsNotFound(err) {
			t.Fatalf("deleting leftover %s credential %s: %v", provider, cr.ID, err)
		}
	}
}
