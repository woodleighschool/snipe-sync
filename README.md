# snipe-sync

[![Release](https://img.shields.io/github/v/release/woodleighschool/snipe-sync?display_name=tag&sort=semver)](https://github.com/woodleighschool/snipe-sync/releases/latest)
[![CI](https://github.com/woodleighschool/snipe-sync/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/woodleighschool/snipe-sync/actions/workflows/ci.yaml)
[![Go](https://img.shields.io/github/go-mod/go-version/woodleighschool/snipe-sync?logo=go)](https://github.com/woodleighschool/snipe-sync/blob/main/go.mod)
[![Container](https://img.shields.io/badge/container-ghcr.io-2496ED?logo=github&logoColor=white)](https://github.com/orgs/woodleighschool/packages/container/package/snipe-sync)
[![License](https://img.shields.io/github/license/woodleighschool/snipe-sync)](https://github.com/woodleighschool/snipe-sync/blob/main/LICENSE)

Reconciles Microsoft Entra users and managed devices from Intune and Jamf Pro into Snipe-IT from one YAML policy. It can run once from the command line or continuously as a service.

The command builds one deterministic plan from authoritative provider state and only writes that exact plan when run in apply mode.

> [!WARNING]
> This project may be unstable or have bugs, use with caution.
> Also expect breaking changes between releases for now.

## 🚀 Usage

Download CLI archives for macOS, Linux, or Windows from the [latest release](https://github.com/woodleighschool/snipe-sync/releases/latest), or use the container `ghcr.io/woodleighschool/snipe-sync:rolling`.

Start with the example policy and an environment file for its `${...}` values:

```bash
cp config.example.yaml config.yaml
touch .env
```

Fill `.env` with values for the `${...}` names in `config.yaml`. The container commands below read this file; export the same values in your shell when using a downloaded binary.

| Command                 | Behaviour                                    |
| ----------------------- | -------------------------------------------- |
| `snipe-sync validate`   | Validate configuration and exit              |
| `snipe-sync plan`       | Print a read-only reconciliation plan        |
| `snipe-sync run --once` | Apply one reconciliation cycle and exit      |
| `snipe-sync run`        | Apply immediately, then continue on interval |

If `config.yaml` is in the current directory, `--config` may be omitted. Multiple `--config` flags apply overlays in order.

### Run once

```bash
snipe-sync run --once
```

The container already selects `run`, so pass only `--once`:

```bash
docker run --rm \
  --env-file .env \
  --volume "$PWD/config.yaml:/config.yaml:ro" \
  ghcr.io/woodleighschool/snipe-sync:rolling \
  --once
```

### Run continuously

```bash
snipe-sync run
```

The container runs continuously by default:

```bash
docker run --rm \
  --env-file .env \
  --volume "$PWD/config.yaml:/config.yaml:ro" \
  ghcr.io/woodleighschool/snipe-sync:rolling
```

Daemon mode writes structured JSON to stderr. Lifecycle and material reconciliation events use `info`, warnings and failures use `warn` or `error`, and successful cycle summaries plus routine no-op evaluations use `debug`.

## ⚙️ Configuration

Configuration is strict and versioned. Unknown fields, missing references, ambiguous Snipe metadata, unset environment placeholders, and invalid CEL expressions fail before reconciliation. Mappings merge recursively; lists and scalar values replace earlier values. Environment placeholders must occupy the whole value, such as `${SNIPEIT_API_KEY}`.

Runtime settings resolve from `SNIPE_SYNC_*` environment variables, then the corresponding YAML value, then the default. CLI flags select configuration files or command behaviour rather than mirroring runtime settings.

| Environment variable                 | YAML fallback             | Default |
| ------------------------------------ | ------------------------- | ------- |
| `SNIPE_SYNC_LOG_LEVEL`               | `log_level`               | `info`  |
| `SNIPE_SYNC_RECONCILE_POLL_INTERVAL` | `reconcile.poll_interval` | `1m`    |

| Section       | Purpose                                                  |
| ------------- | -------------------------------------------------------- |
| `connections` | Microsoft Graph, Jamf, and Snipe-IT API credentials      |
| `identity`    | Internal domains and Entra group aliases                 |
| `devices`     | Intune and Jamf sources, precedence, and managed-by text |
| `target`      | Snipe-IT connection and checkout timezone                |
| `reconcile`   | Interval between completed reconciliation cycles         |
| `users`       | Selection, location, and disabled-user policy            |
| `assets`      | Eligibility, status, assignment, and absence policy      |

User selection, first-match location rules, and asset field skip rules use typed CEL expressions. Each `assets.skip` rule has a `when` condition and one or more `fields`: `name`, `managed_by`, or `assignment`. Matching rules suppress only fields that would otherwise change. Human-readable Snipe departments, locations, manufacturers, statuses, and the managed-by custom-field label are resolved from each complete target snapshot. Numeric IDs and generated custom-field column names do not belong in configuration.

[`snipe-sync.schema.json`](snipe-sync.schema.json) provides the structural YAML contract for editors.

## 🔄 Reconciliation

The process bootstraps a complete Entra user snapshot with a delta query, then advances an in-memory snapshot and cursor on later cycles. Configured group aliases use transitive group-member snapshots, avoiding a membership request for every user. User state and its cursor advance together only after the full delta round and all group snapshots complete; an expired cursor triggers a fresh bootstrap. Restarting the process also performs a fresh bootstrap.

Intune, Jamf, and Snipe-IT remain complete bulk snapshots each cycle. Cycles run serially, and the poll interval starts after a cycle finishes. If any configured source fails or returns an incomplete page set, that cycle performs no writes.

Users are created first, followed by updates and disables. IDs returned for new users are then available to asset assignment. All asset patches complete before check-ins begin, and all check-ins complete before checkouts begin. An item failure stops later actions for that asset while independent items continue.

Human plans include user counts and device rows that change or require attention. Use `plan --all` to include unchanged devices. JSON output always contains the complete structured plan.

## 🧑‍💻 Development

Mise owns the toolchain and repository checks:

```bash
mise run build
mise run generate
mise run test
mise run lint
mise run fmt-check
mise run workflow-lint
mise run vulncheck
```

Tests use local servers and synthetic identities; provider credentials are not required.

Review the plan before using `run` against a live Snipe-IT instance.

## 📄 License

Licensed under the [Apache License 2.0](LICENSE).
