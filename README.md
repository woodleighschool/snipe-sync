# snipe-sync

Reconciles Microsoft Entra users and managed devices from Intune and Jamf Pro into Snipe-IT from one YAML policy.

`snipe-sync` reads every configured source, builds one deterministic plan, and only writes that exact plan when explicitly run with `run --once`.

> [!WARNING]
> This project may be unstable or have bugs, use with caution.
> Also expect breaking changes between releases for now.

## 🚀 Usage

Download an archive from the [latest release](https://github.com/woodleighschool/snipe-sync/releases/latest), or build it with Mise. Start from [`config.example.yaml`](config.example.yaml). If `config.yaml` is present in the current directory, `--config` may be omitted:

```bash
snipe-sync validate
snipe-sync plan
snipe-sync plan --output json
snipe-sync run --once
```

Multiple `--config` flags apply overlays in order. `plan` is always read-only. Applying requires the explicit `--once` flag; there is no continuous mode.

The published container runs one reconciliation by default:

```bash
docker run --rm \
  --env-file .env \
  --volume "$PWD/config.yaml:/config.yaml:ro" \
  ghcr.io/woodleighschool/snipe-sync:rolling \
  run --config /config.yaml --once
```

`snipe-sync` has no interval loop or local reconciliation state. Use a scheduler for cadence. Each apply run lists changes and errors; unchanged and skipped devices are reported only in the summary, avoiding repetitive per-device log noise.

## ⚙️ Configuration

Configuration is strict and versioned. Unknown fields, missing references, ambiguous Snipe metadata, unset environment placeholders, and invalid CEL expressions fail before reconciliation. Mappings merge recursively; lists and scalar values replace earlier values. Environment placeholders must occupy the whole value, such as `${SNIPEIT_API_KEY}`.

| Section       | Purpose                                                  |
| ------------- | -------------------------------------------------------- |
| `connections` | Microsoft Graph, Jamf, and Snipe-IT API credentials      |
| `identity`    | Internal domains and Entra group aliases                 |
| `devices`     | Intune and Jamf sources, precedence, and managed-by text |
| `target`      | Snipe-IT connection and checkout timezone                |
| `users`       | Selection, location, and disabled-user policy            |
| `assets`      | Eligibility, status, assignment, and absence policy      |

User selection, first-match location rules, and shared-device preservation use typed CEL expressions. Human-readable Snipe departments, locations, manufacturers, statuses, and the managed-by custom-field label are resolved from each complete target snapshot. Numeric IDs and generated custom-field column names do not belong in configuration.

[`snipe-sync.schema.json`](snipe-sync.schema.json) provides the structural YAML contract for editors.

## 🔄 Reconciliation

Each invocation reads complete Entra, Intune, Jamf, and Snipe-IT snapshots before calculating user and asset decisions. If any configured top-level source fails or returns an incomplete page set, the command exits before writes. An individual optional Entra group lookup may warn and preserve the current Snipe location while safe user fields continue to reconcile.

Users are created first, followed by updates and disables. IDs returned for new users are then available to asset assignment. All asset patches complete before check-ins begin, and all check-ins complete before checkouts begin. An item failure stops later actions for that asset while independent items continue.

Human plans include user counts and a complete device table. JSON output is one structured plan object suitable for automated comparison.

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

Review the plan before using `run --once` against a live Snipe-IT instance.

## 📄 License

Licensed under the [Apache License 2.0](LICENSE).
