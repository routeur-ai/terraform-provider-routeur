package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/routeur-ai/terraform-provider-routeur/internal/client"
)

// Destroy resets the singleton to the API default rather than removing it.
func testAccCheckModerationSettingsReset(t *testing.T) resource.TestCheckFunc {
	return func(*terraform.State) error {
		c, org := testAccClient(t)
		s, err := client.Get[apiModerationSettings](context.Background(), c, client.Path("orgs", org, "moderation-settings"))
		if err != nil {
			return err
		}
		if s.StreamingModeration != "buffered" {
			return fmt.Errorf("streaming_moderation is %q after destroy, want buffered", s.StreamingModeration)
		}
		return nil
	}
}

func testAccModerationSettingsConfig(mode string) string {
	return fmt.Sprintf(`
resource "routeur_moderation_settings" "test" {
  streaming_moderation = %q
}`, mode)
}

func TestAccModerationSettings_basic(t *testing.T) {
	const res = "routeur_moderation_settings.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckModerationSettingsReset(t),
		Steps: []resource.TestStep{
			{
				Config: testAccModerationSettingsConfig("chunked"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(os.Getenv(EnvOrg))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("streaming_moderation"), knownvalue.StringExact("chunked")),
				},
			},
			{ResourceName: res, ImportState: true, ImportStateVerify: true, ImportStateId: os.Getenv(EnvOrg)},
			{
				Config: testAccModerationSettingsConfig("buffered"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
			},
			{
				Config: testAccModerationSettingsConfig("chunked"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
			},
			{
				Config:           testAccModerationSettingsConfig("chunked"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

// Out-of-band change is detected as drift and put back.
func TestAccModerationSettings_drift(t *testing.T) {
	const res = "routeur_moderation_settings.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckModerationSettingsReset(t),
		Steps: []resource.TestStep{
			{Config: testAccModerationSettingsConfig("chunked")},
			{
				PreConfig: func() {
					c, org := testAccClient(t)
					err := c.Do(context.Background(), http.MethodPut, client.Path("orgs", org, "moderation-settings"), map[string]any{"streaming_moderation": "buffered"}, nil)
					if err != nil {
						t.Fatal(err)
					}
				},
				Config: testAccModerationSettingsConfig("chunked"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)},
				},
			},
		},
	})
}

func TestAccModerationSettings_validation(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      testAccModerationSettingsConfig("live"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config:        testAccModerationSettingsConfig("chunked"),
				ResourceName:  "routeur_moderation_settings.test",
				ImportState:   true,
				ImportStateId: "someone-else",
				ExpectError:   regexp.MustCompile(`expected the organisation slug`),
			},
		},
	})
}

func init() {
	registerSpecCoverage(specCoverage{
		typeName:   "routeur_moderation_settings",
		schema:     "ModerationSettings",
		operations: []string{"getModerationSettings", "replaceModerationSettings"},
		// id is the organisation slug, not an API property.
	})
}
