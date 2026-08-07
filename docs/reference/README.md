# Reference Contract Root

`docs/reference` is the durable governance contract root for gofly.

Files in this directory are not local planning notes. They are machine-readable
or script-verified evidence consumed by Make targets and `bin/scripts/check-*.sh`
governance gates. Do not delete this directory unless every consuming script,
Make target, `sourceOfTruth`, and `evidenceRefs` entry has first been migrated
to a new durable contract root.

Allowed content:

- JSON manifests with stable `schema` fields and runnable acceptance gates.
- Markdown contracts that are directly checked by governance scripts.
- Small golden/reference files explicitly named by a gate.

Disallowed content:

- Agent planning notes.
- Runtime evidence from `.aiflow`, `.harness`, `.tmp-test`, or local temp dirs.
- Benchmark run output such as `bench/current.txt`.
- Generated project output that should live in temporary directories.

If a new file is added here, it must be referenced by a script, Make target, or
another tracked contract file.
