# goctl Replay Rollback

If this replay stops generating deterministic artifacts or the generated module
does not compile, pin the previous `gozero-compatible` generator profile and
keep the last passing model/cache template. Treat unclassified diffs as
breaking candidates until a migration note explains whether the change is a
compatible addition, a generated cache template change, or an intentional
breaking change.
