package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// One credential per provider per organisation: these tests never use the
// shared provider (openai, see testAccEnsureCredential), and each claims
// its providers with testAccFreeProvider, so the suite runs in any order in
// one organisation.

func testAccCheckProviderCredentialGone(t *testing.T) resource.TestCheckFunc {
	return testAccCheckGone(t, "routeur_provider_credential", func(org string, a map[string]string) string {
		return client.Path("orgs", org, "provider-credentials", a["id"])
	})
}

func testAccProviderCredentialConfig(body string) string {
	return fmt.Sprintf(`
resource "routeur_provider_credential" "test" {
  %s
}
`, body)
}

// testAccImportIDAttr returns the id attribute, for org-level imports.
func testAccImportIDAttr(resourceName string) func(*terraform.State) (string, error) {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return "", fmt.Errorf("%s not in state", resourceName)
		}
		return rs.Primary.ID, nil
	}
}

func TestAccProviderCredential_basic(t *testing.T) {
	const (
		res   = "routeur_provider_credential.test"
		key1  = "sk-test-mistral-aaaaaaaaaaaa1111"
		key2  = "sk-test-mistral-bbbbbbbbbbbb2222"
		other = "sk-test-mistral-cccccccccccc3333"
	)
	sameID := statecheck.CompareValue(compare.ValuesSame())
	var credID string // for the out-of-band rotation
	testAccFreeProvider(t, "mistral")
	testAccFreeProvider(t, "deepseek")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckProviderCredentialGone(t),
		Steps: []resource.TestStep{
			// Create.
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name = "mistral"
  label         = "prod"
  key           = %q
`, key1)),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^cred_[1-9][0-9]*$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("provider_name"), knownvalue.StringExact("mistral")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("fingerprint"), knownvalue.StringExact("sk-test…1111")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					sameID.AddStateValue(res, tfjsonpath.New("id")),
				},
			},
			// The key is never returned, so import cannot verify it.
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportIDAttr(res), ImportStateVerifyIgnore: []string{"key"}},
			// In place: relabel, disable, rotate the key. Same id.
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name = "mistral"
  label         = "production"
  key           = %q
  enabled       = false
`, key2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("label"), knownvalue.StringExact("production")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("fingerprint"), knownvalue.StringExact("sk-test…2222")),
					sameID.AddStateValue(res, tfjsonpath.New("id")),
				},
			},
			// Removing `enabled` enables it again; the key is not re-sent.
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name = "mistral"
  label         = "production"
  key           = %q
`, key2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("fingerprint"), knownvalue.StringExact("sk-test…2222")),
				},
				Check: func(s *terraform.State) error {
					credID = s.RootModule().Resources[res].Primary.ID
					return nil
				},
			},
			// Someone rotates the key outside Terraform: the new fingerprint
			// makes the plan send the configured key again.
			{
				PreConfig: func() {
					c, org := testAccClient(t)
					if err := c.Do(context.Background(), http.MethodPatch, client.Path("orgs", org, "provider-credentials", credID), map[string]any{"key": other}, nil); err != nil {
						t.Fatalf("rotating %s outside Terraform: %v", credID, err)
					}
				},
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name = "mistral"
  label         = "production"
  key           = %q
`, key2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("fingerprint"), knownvalue.StringExact("sk-test…2222")),
					sameID.AddStateValue(res, tfjsonpath.New("id")),
				},
			},
			// provider_name is create-only: changing it replaces the credential.
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name = "deepseek"
  label         = "production"
  key           = %q
`, key2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("provider_name"), knownvalue.StringExact("deepseek")),
				},
			},
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name = "deepseek"
  label         = "production"
  key           = %q
`, key2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// key_wo: the key is never in the plan or state; key_wo_version sends it.
func TestAccProviderCredential_writeOnly(t *testing.T) {
	testAccFreeProvider(t, "anthropic")
	const (
		res  = "routeur_provider_credential.test"
		key1 = "sk-ant-test-aaaaaaaaaaaaaaaa1111"
		key2 = "sk-ant-test-bbbbbbbbbbbbbbbb2222"
	)
	sameID := statecheck.CompareValue(compare.ValuesSame())

	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckProviderCredentialGone(t),
		Steps: []resource.TestStep{
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name  = "anthropic"
  label          = "prod"
  key_wo         = %q
  key_wo_version = 1
`, key1)),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("key_wo"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("key"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("key_wo_version"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("fingerprint"), knownvalue.StringExact("sk-ant-…1111")),
					sameID.AddStateValue(res, tfjsonpath.New("id")),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateIdFunc: testAccImportIDAttr(res), ImportStateVerifyIgnore: []string{"key_wo_version"}},
			// A new key_wo alone changes nothing: Terraform cannot see it.
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name  = "anthropic"
  label          = "prod"
  key_wo         = %q
  key_wo_version = 1
`, key2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Bumping key_wo_version sends it: rotated in place.
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name  = "anthropic"
  label          = "prod"
  key_wo         = %q
  key_wo_version = 2
`, key2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("fingerprint"), knownvalue.StringExact("sk-ant-…2222")),
					sameID.AddStateValue(res, tfjsonpath.New("id")),
				},
			},
			// Switching to `key` with the same key: an update the server
			// treats as a no-op; the fingerprint stays.
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name = "anthropic"
  label         = "prod"
  key           = %q
`, key2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("fingerprint"), knownvalue.StringExact("sk-ant-…2222")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("key_wo_version"), knownvalue.Null()),
					sameID.AddStateValue(res, tfjsonpath.New("id")),
				},
			},
			{
				Config: testAccProviderCredentialConfig(fmt.Sprintf(`
  provider_name = "anthropic"
  label         = "prod"
  key           = %q
`, key2)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// API refusals land on the right attribute: a key the provider rejects
// (on key) and a second credential for one provider (on provider_name).
func TestAccProviderCredential_refused(t *testing.T) {
	testAccFreeProvider(t, "google")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckProviderCredentialGone(t),
		Steps: []resource.TestStep{
			{
				Config: testAccProviderCredentialConfig(`
  provider_name = "google"
  label         = "prod"
  key           = "AIza-invalid-key-aaaaaaaa"
`),
				ExpectError: regexp.MustCompile(`(?s)with routeur_provider_credential.test,.*key +=.*Google rejected this API key \(401\)`),
			},
			{
				Config: `
resource "routeur_provider_credential" "a" {
  provider_name = "google"
  label         = "a"
  key           = "AIza-test-key-aaaaaaaa1111"
}

# Sequential: the API checks one-per-provider without a lock, so two
# concurrent creates can both succeed (reported upstream).
resource "routeur_provider_credential" "b" {
  depends_on    = [routeur_provider_credential.a]
  provider_name = "google"
  label         = "b"
  key           = "AIza-test-key-bbbbbbbb2222"
}
`,
				ExpectError: regexp.MustCompile(`(?s)provider_name.*already has a credential for Google.*terraform import\s+routeur_provider_credential.<name>\s+cred_<N>`),
			},
			// A provider this version does not know is only a warning at plan
			// time.
			{
				Config: `
resource "routeur_provider_credential" "a" {
  provider_name = "no-such-provider"
  label         = "a"
  key           = "sk-test-aaaaaaaa"
}
`,
				ExpectError: regexp.MustCompile(`(?s)Could not create no-such-provider credential.*provider_name`),
			},
		},
	})
}

func TestAccProviderCredential_validation(t *testing.T) {
	steps := []struct{ body, err string }{
		{`provider_name = "openai"
  label  = "prod"
  key    = "sk-test-aaaaaaaa"
  key_wo = "sk-test-aaaaaaaa"
  key_wo_version = 1`, `(?s)Invalid Attribute Combination.*Exactly one of these attributes must be configured: \[key,key_wo\]`},
		{`provider_name = "openai"
  label = "prod"`, `Exactly one of these attributes must be configured: \[key,key_wo\]`},
		{`provider_name = "openai"
  label  = "prod"
  key_wo = "sk-test-aaaaaaaa"`, `These attributes must be configured together: \[key_wo,key_wo_version\]`},
		{`provider_name = "openai"
  label = "prod"
  key   = "sk-test-aaaaaaaa\n"`, `key must not start or end with whitespace`},
		{`provider_name = "openai"
  label = "prod"
  key   = "short"`, `character count must be between 8 and 512`},
	}
	var ts []resource.TestStep
	for _, s := range steps {
		ts = append(ts, resource.TestStep{
			Config:      testAccProviderCredentialConfig(s.body),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(s.err),
		})
	}
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    ts,
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_provider_credential",
		schema:     "ProviderCredential",
		operations: []string{"createProviderCredential", "getProviderCredential", "updateProviderCredential", "deleteProviderCredential"},
		aliases:    providerNameAlias,
	})
}

// Interpolating the credential's provider_name into `model` orders creation
// (credential first) and destruction (credential last) without depends_on.
func TestAccProviderCredential_workspaceModelReference(t *testing.T) {
	testAccFreeProvider(t, "mistral")
	ws := testAccSlug("ws")
	config := fmt.Sprintf(`
resource "routeur_provider_credential" "test" {
  provider_name = "mistral"
  label         = "reference"
  key           = "sk-test-mistral-dddddddddddd4444"
}

resource "routeur_workspace" "test" {
  slug = %q
  name = "Credential reference"
}

resource "routeur_workspace_model" "test" {
  workspace_id = routeur_workspace.test.id
  model        = "${routeur_provider_credential.test.provider_name}:gpt-4o"
}
`, ws)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckProviderCredentialGone(t), testAccCheckWorkspaceGone(t)),
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("routeur_workspace_model.test", tfjsonpath.New("model"), knownvalue.StringExact("mistral:gpt-4o")),
					statecheck.ExpectKnownValue("routeur_workspace_model.test", tfjsonpath.New("active"), knownvalue.Bool(true)),
				},
			},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}
