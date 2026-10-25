# id: the credential id. The key cannot be read back: the first apply
# after an import sends the configured key once (nothing changes if it is
# the stored key).
import {
  to = routeur_provider_credential.openai
  id = "cred_7"
}
