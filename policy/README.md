# Static build policy

The `policy` package checks `.fw` source before a build. It reports source lines for denied operations. It does not confine a running binary.

```json
{
  "allowedImports": ["fmt", "os", "m31labs.dev/ferrous-wheel/hostfs"],
  "denyProcess": true,
  "denyNetwork": true,
  "fileRoots": ["/srv/agent-work"]
}
```

- `allowedImports` names exact source imports. Omit it to allow all imports. Use `[]` to deny all imports.
- `denyProcess` rejects direct process APIs and process-capable imports.
- `denyNetwork` rejects direct calls and network-capable imports.
- `fileRoots` checks literal absolute paths in direct file calls. Omit it to allow those calls. Use `[]` to deny them.

Call `ParseConfig`, then `Check` for each source file before transpilation or module loading. Stop the build if `Check` returns diagnostics. Print each diagnostic with `Error()`.

The checker does not follow symlinks or inspect imported package code, reflection, or child process effects. Dynamic file paths fail when `fileRoots` is set. File roots use the build host's path rules. Cross-target file roots are unsupported when the target uses different path rules. This policy is a build gate, not a sandbox.
