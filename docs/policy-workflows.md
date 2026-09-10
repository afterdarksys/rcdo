# Scriptable policy workflows

## Starter packs

`examples/rego/starter` contains five composable rules: required Owner/Environment
tags, public exposure, approved regions, deletion/replacement/forgetting, and
production approval observations. Load `base.rego` with one or more rules:

```sh
rcdo rego-check --rego examples/rego/starter/base.rego \
  --rego examples/rego/starter/tags.rego \
  --rego examples/rego/starter/public.rego \
  --input examples/rego/starter/pass.json
```

Each rule has a `<rule>-fail.json` example; `pass.json` passes all five. These are
editable organization policy examples. The region allowlist is us-east-1/us-west-2;
production is the exact environment name `production`.

Input is a normalized observation with this shape:

```json
{"environment":"development","approved":false,"resources":[
  {"address":"example.bucket","tags":{"Owner":"platform","Environment":"development"},
   "region":"us-east-1","public":false,"actions":["create"]}
]}
```

Resource addresses must be unique. All shown fields are required. Missing or
malformed facts yield incomplete status. `public` and `approved` are supplied
observations; these packs do not discover effective exposure or verify approval
authenticity. Keep supporting evidence in your review. Empty resource lists are
valid and mean the supplied scope contains no resources. Tags/regions/public
checks apply to every listed resource, including deletions.

These normalized packs do not accept raw provider responses or raw Terraform
plans. For direct saved-plan deletion review use `examples/rego/terraform.rego`.
The [Rego reference](rego.md) documents execution limits and exit statuses.
