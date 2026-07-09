# CAAM Local Retirement Preservation

This directory preserves the material local-only artifacts that existed before
retiring the local `/data/projects/caam` and `/data/projects/caam-localfix`
workspaces in favor of the upstream `caam` binary at commit `98c05c7`.

## Current Binary State

At preservation time, `caam` on `PATH` resolved to:

```text
/home/ubuntu/.local/bin/caam
caam v0.1.12-1-g98c05c7 (98c05c7) built on 2026-07-08T12:00:19Z with go1.24.4
```

The fork checkout was at:

```text
local main  = bd15600f47127b346947045c070e0d879140762c
origin/main = bd15600f47127b346947045c070e0d879140762c
upstream    = 98c05c7bf78438ef2c4b2829c47d8d024bed0872
```

## Preserved Content

- `caam-scratch/`: ignored scratch notes, oracle consults, bead planning
  diagnostics, and localfix patch copies from `/data/projects/caam/scratch`.
- `caam-localfix/`: root-level operational docs, reconciler script, patch files,
  AGENTS guidance, and reconciler log from `/data/projects/caam-localfix`.

## Binary Inventory Not Committed

The old localfix binaries were not committed because they are large build
artifacts and the active installed binary is now upstream `98c05c7`. Their
identity was preserved here instead:

```text
da78c017ba80f262bfc487a8ce865d1344ed85d88550aeb0eeb79ebcc26fc829  /data/projects/caam-localfix/caam.installed-backup.20260624-021203
1b8cd449bd8da3bc6eddbd4bc2fc418aa347248dbe020740231597e5880b08d6  /data/projects/caam-localfix/caam-src/caam
```

File metadata:

```text
/data/projects/caam-localfix/caam.installed-backup.20260624-021203: ELF 64-bit LSB executable, x86-64, statically linked, stripped
/data/projects/caam-localfix/caam-src/caam: ELF 64-bit LSB executable, x86-64, dynamically linked, stripped
```

The nested localfix source checkout was a detached upstream checkout:

```text
caam-localfix/caam-src HEAD        = 304e037569802c887e3548ea8bdc8cdb76db8d22
caam-localfix/caam-src origin/main = 304e037569802c887e3548ea8bdc8cdb76db8d22
git status                        = ## HEAD (no branch)
```

That source state is recoverable from upstream history; the unique local value
was in the operational docs, scripts, patches, and logs preserved here.
