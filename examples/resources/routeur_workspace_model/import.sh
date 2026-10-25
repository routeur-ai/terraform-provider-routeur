# <workspace_id>/<provider>:<model_id>; the model id may itself contain "/".
terraform import 'routeur_workspace_model.openai["gpt-4o"]' ws_3/openai:gpt-4o
terraform import 'routeur_workspace_model.openai["ft/custom-model"]' ws_3/openai:ft/custom-model
