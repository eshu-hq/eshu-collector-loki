# eshu-collector-loki

Status: pre-release. It isn't the live Loki producer for Eshu yet, and Eshu core still owns admission and storage.

This collector takes a quick, careful look at a live Loki target and reports what it finds as facts, the small checked records Eshu stores as evidence. It reads labels, series shape, and ruler rules. Log lines themselves never leave Loki through this code, which is very much the point.

```mermaid
flowchart LR
    A["Loki API"] --> B["This collector"]
    B --> C["Eshu extension host"]
    C --> D["Eshu core"]
```

In words: Loki answers, this collector translates, the host checks the result, and core decides what gets stored. The host drives everything one claim at a time. (A claim is one bounded unit of work, if you're new to the vocabulary.)

## When this earns its keep

Use it when you want Eshu to know which log labels, series, and ruler rules a Loki target really serves right now, not just what a config file promised last quarter.

Don't use it to search logs, export log content, or run LogQL for people. It only reads metadata endpoints, and it writes nothing back to Loki.

## Quickest path to a running collector

Eshu's extension host runs the binary for you, but you can smoke-test it locally with a recorded request:

```bash
collector --sdk-stdio < request.json > result.json
```

One request goes in on stdin, one result comes out on stdout, and that's the only serving mode. For a real deployment you'll seed the binary from the release image and copy `config.example.yaml` into an activation. Both steps sit in [docs/guides/operations.md](docs/guides/operations.md) and [docs/reference/configuration.md](docs/reference/configuration.md).

## Where to read next

| You're trying to… | Open |
| --- | --- |
| understand what a claim turns into | [docs/architecture/README.md](docs/architecture/README.md) |
| fill in an activation file | [docs/reference/configuration.md](docs/reference/configuration.md) |
| run, ship, or debug the thing | [docs/guides/operations.md](docs/guides/operations.md) |

The files the docs keep pointing at: [config.example.yaml](config.example.yaml), [grant.example.json](grant.example.json), [manifest.yaml](manifest.yaml), and the [Dockerfile](Dockerfile).

## Related repos

- [eshu](https://github.com/eshu-hq/eshu): core and the extension host that runs this collector.
- [eshu-sdk](https://github.com/eshu-hq/eshu-sdk): the collector and fact-schema modules this repo pins.
- [eshu-deploy](https://github.com/eshu-hq/eshu-deploy): deployment wiring for Eshu components.

## A 30-second mental model

If you remember one thing, make it this: shape, not content. The collector tells Eshu which labels, series, and rules exist, and it leaves log lines, raw LogQL, and tenant details out of every fact. When its view gets clipped, you'll see a coverage warning instead of silence. That's the contract, and the docs pages above fill in the how.

Manifest version today is `0.1.2` for collector kind `loki`.
You'll find the exact SDK pins and release flow in [docs/guides/operations.md](docs/guides/operations.md).
If a fact kind ever changes, the manifest and [docs/architecture/README.md](docs/architecture/README.md) change together.

## Developing

One line version: run `make ci` before you push, and let [docs/guides/operations.md](docs/guides/operations.md) fill in the rest.

## Contributing, security, license

Start with [CONTRIBUTING.md](CONTRIBUTING.md). The license is MIT, see [LICENSE](LICENSE). There's no `SECURITY.md` in this repo, so please don't invent a disclosure address from memory.
