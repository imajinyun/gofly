# REST Middleware Profiles

schema: gofly.rest_middleware_profiles.v1

Generated REST services expose three named middleware profiles. The profile is
stored in generated config as `rest.preset` and is visible through runtime and
control-plane snapshots.

## Profile Matrix

| Profile | Generated style | Default middleware contract |
| --- | --- | --- |
| `minimal` | `--style minimal` | `recover`, `health`, `requestId`, plus the global max-body safety limit. |
| `standard` | `--style basic` and default `gofly new api` | `minimal` plus `trace`, `log`, `timeout`, and `metrics`. Resilience middleware remains explicit opt-in. |
| `production` | `--style production` and default `gofly new service` | `standard` plus `rateLimit`, `adaptiveRateLimit`, `maxConcurrency`, `breaker`, `securityHeaders`, and log redaction defaults. |

Legacy `development` keeps the standard low-friction behavior. Legacy `custom`
keeps caller-owned middleware behavior except for the max-body safety limit.

## Verification

The focused gate is:

```sh
make rest-profile-check
```

The gate verifies:

- `rest.NewServer` resolves `minimal`, `standard`, and `production` profiles.
- `app.ServiceConf.RESTConfig` preserves explicit profile choices.
- generated `minimal`, `basic`, and `production` projects write the expected
  `rest.preset` and runtime middleware chain.

The broader generated-output gate also depends on this contract:

```sh
make generated-output-governance
```

## Compatibility Rules

1. `production` may add safe middleware but must not remove resilience or
   security headers without a breaking-change report.
2. `standard` must not silently enable rate limiters, breakers, adaptive
   limiters, or max-concurrency guards; callers must opt in.
3. `minimal` must stay suitable for small local examples and must not require
   admin, RPC, discovery, or external services.
4. Generated config must keep `rest.preset` explicit so automation can inspect
   the selected profile without parsing middleware booleans.
