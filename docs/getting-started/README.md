# Getting started

Eshu's extension host normally runs this binary for you. Getting started means three things: seed the binary, write an activation, and know how to smoke-test it by hand.

## 1. Smoke-test the binary (one line)

```bash
collector --sdk-stdio < request.json > result.json
```

One request goes in on stdin, one result comes out on stdout. That is the only serving mode — no daemon, no HTTP server.

## 2. Seed it for the host

The release image is `ghcr.io/eshu-hq/eshu-collector-loki`. An init container (the setup container that runs before the main one) installs the binary and manifest for the host:

```bash
/usr/local/bin/collector seed --dest DIR [--image-ref REPO@sha256:DIGEST]
```

The full seed contract lives in the [operations guide](../guides/operations.md).

## 3. Write the activation

Copy [config.example.yaml](../../config.example.yaml), name your Loki target under `targets`, and keep `host.sourceSystem: loki`. Secrets travel by environment-variable name only (`token_env`, `tenant_id_env`) — never write a value in the file. Every key is in [Configuration](../reference/configuration.md).

## 4. Grants before activation

All four fact kinds belong to Eshu core, so the host wants a live producer grant per kind before it activates the component. [grant.example.json](../../grant.example.json) lists the four. Details sit in [Configuration](../reference/configuration.md).

## Where next

- How a claim becomes facts: [Architecture](../architecture/README.md).
- Running, signals, and trouble: the [operations guide](../guides/operations.md).
