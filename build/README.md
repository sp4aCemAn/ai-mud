# Building & Running ai-mud

One-stop doc for the host scripts and the container stack. The test
chart (what guards what) lives in `tests/TESTMAP.md`; the deployed
gotchas live in `tests/DEBUG_STATE.md`. This file covers **operating
the thing**: install → develop → play → deploy.

## The stack, in one picture

```
HMAC host (your Mac)
├── cactus serve :1243        the game master's local brain (host binary,
│                              NOT a container — Metal/CPU kernels live here)
└── docker compose (stack)
    ├── build-postgres-1  :5432   relational: world records, world_objects,
    │                             accounts'{not served here}, generations
    │                             ledger, narr_revisions (lore commits)
    ├── build-docdb-1     :5433   jsonb docs: accounts + credentials
    └── build-gamemaster-1  :2525 SSH game · :8081 HTTP tools/API
        (built from build/gamemaster/Dockerfile — scratch, CGO_ENABLED=0)
```

## Scripts

| Script | What it does |
|---|---|
| Script | What it does |
|---|---|
| `build/install.sh` | Installs the HOST dependencies via Homebrew: cactus (the GM engine), expect (SSH UX smokes), golangci-lint (lint CI parity), docker (skipped when present). Go + module deps ride the Dockerfiles/dev flow, not brew. (Linux: cactus comes from source — see the quickstart link inside.) |
| `build/compose.podman.yaml` | **Podman override** — layer it on the base compose (`podman-compose -f compose.yaml -f compose.podman.yaml up -d --build gamemaster`, or docker with `:-f` too). Carries ONLY the podman deltas: env re-pointed to `host.containers.internal`, `extra_hosts` gateway entries, and the note that cactus must bind `0.0.0.0` on Linux. Docker merges it harmlessly for testing. |
| `build/cactus.sh` | `start / stop / status / models / ping / log` for the GM engine (`cactus serve google/gemma-4-E2B-it --no-cloud-handoff --port 1243`). Portable macOS + Linux: finds the binary (brew paths or PATH), the pid-file lives at `~/.cactus-serve.pid`, log at `~/.cactus-serve.log` (`log [n]` tails it). **On Linux + podman raise it with `CACTUS_HOST=0.0.0.0`** — rootless podman's bridge reaches the host's gateway IP, not its loopback. Cloud handoff is **pinned OFF**; `stop` even catches untracked instances (bare nohup / brew services) by name. |
| `build/deploy.sh` | Rebuilds + deploys the stack. **The compose-cache gotcha**: it runs a real `compose build` FIRST, compares the running image stamp to the just-built one and force-recreates on mismatch (plain `up -d --build` silently reused an old image once). Waits for `/healthz`, tells you whether cactus is up. |
| `build/dev.sh` | The TESTMAP verification stack, scriptified: gofmt (dirty files = format in place), vet, build, `go test ./... -count=1`, then — when the containers are live — `STORAGE_INTEGRATION` + the deployed-contract smoke suite (`GAME_SMOKE_LIVE_ONLY=1`). Green across the board or it stops. |

## Fresh-machine quickstart

```sh
build/install.sh                 # brew deps: docker, cactus, expect, golangci-lint
build/cactus.sh start            # the GM engine boots + downloads the model once (~2.5G)
build/cactus.sh ping             # "ok" — the engine talks
build/deploy.sh                  # the stack builds + boots; /healthz answers
# play:
ssh localhost -p 2525            # (or any key — auto-mints an account)
curl http://127.0.0.1:8081/api/world   # the ops readback

# the verification loop (after every change):
build/dev.sh                     # fmt, vet, unit tests, (+integration when containers up)
```

**Linux / podman instead of Docker Desktop:**

```sh
# 1) the engine smiles at the bridge, not its loopback:
CACTUS_HOST=0.0.0.0 build/cactus.sh start
# 2) the stack layers the podman override (env re-pointing + gateway DNS):
cd build
podman-compose -f compose.yaml -f compose.podman.yaml up -d --build
#    (or docker/podman via socket: DOCKER_HOST=unix:///run/user/$UID/podman/podman.sock
#     docker compose -f compose.yaml -f compose.podman.yaml up -d --build)
```

> `deploy.sh`/`dev.sh` shell out to `docker compose -f compose.yaml`; on podman either swap the engine by exporting `DOCKER_HOST` to the podman socket, or invoke the two `-f`-layered forms by hand (the override makes both engines read the same stack).

## Day-to-day

- **Code changed**: `build/dev.sh` → `build/deploy.sh`. The deploy script
  nails the stale-image trap; the dev script keeps fmt/vet green.
- **Deploy without touching the DB**: `docker compose -f build/compose.yaml
  restart gamemaster` reboots the game only (rows replay, players drop).
- **Config lives in `configs/`**: harness persona/model (`harness.yaml`),
  the GM's verb card (`gm_tools.json` — OpenAI function-calling format;
  the interpreter stage reads it; removal-class verbs are
  deliberately NOT exposed to any model).
- **Junk ecology**: CI smoke journeys persist `smoke:*` and
  `uX:harness-test-group` rows + villages into the live world. Sweep by
  SQL with an explicit whitelist (do NOT delete with bare
  `... like 'smoke%'` — the world row is NAMED smokeworld, and a strip
  like that once nearly ate it).

## Environment knobs (compose env / harness config)

| Knob | Meaning | Today's value |
|---|---|---|
| `POSTGRES_DSN` / `DOCUMENT_DSN` | the two DB backends | `postgres` + `docdb` service names |
| `HARNESS_BASE_URL` | the GM persona stage (OpenAI-compatible) | `http://host.docker.internal:1234/v1` (LM Studio, gemma-4-e4b) |
| `HARNESS_INTERPRETER_BASE_URL` | the tool-call interpreter stage | `http://host.docker.internal:1243/v1` (cactus) |
| `HARNESS_MODEL` / `HARNESS_INTERPRETER_MODEL` | the two models | `google/gemma-4-e4b` / `gemma-4-e2b-it-cq4` |
| `HARNESS_COOLDOWN` | the shortest interval between two GM turns | `15s` (the poke-storm rail; raise it to slow the GM's world-building) |
| `W_SEED` | seed-flow world's seed | `20260909` (smoke-map pinned) |
| harness `cadence` | the heartbeat floor | 45s (the trigger is the EXPLORE event — see TESTMAP's frontier section) |
| harness `tools_path` | the verb card | `configs/gm_tools.json` (absent = single-stage text protocol) |

## Where the game master's logs live

| Surface | What you see | Where |
|---|---|---|
| Game master loop | every cycle: `cycle begins` (events/players/towns), `decision` (persona turn landed), `gm spawned` / `gm raised a village` (rails placed content — with coords), `announces` (what players heard), reject reasons | **`docker logs build-gamemaster-1`** (the gamemaster's stdout; slog stderr — everything greppable) |
| The turn ledger | one audit row per GM turn (model, prompt, output note: no-op / applied=N / declined) | **`docker exec build-postgres-1 psql -U aimud -d aimud -c "select * from generations order by id desc limit 10;"`** |
| Cactus (interpreter) | the servant process: model loads, request starts, `tool_calls` replies | **`~/.cactus-serve.log`** (started by `build/cactus.sh start`; `build/cactus.sh ping` = quick smoke) |
| LM Studio (persona) | prompt cache, reasoning time, the persona's prose + tool_calls shape | the **LM Studio desktop app**'s server console (it intros the `/v1/chat/completions` traffic + model runs) |
| In-world debug rails | every content verb announces its coordinates to players: `(at x,y) a band of … stirs` / `(at x,y) the gates of NAME rise…` | the SSH event pane (and the announce lines land in both `docker logs` and players' event logs) |

## What lives where (packaging story, briefly)

- **The Go server ships as one static binary in a scratch container**
  (`build/gamemaster/Dockerfile`): trivial image, deps all vendored in the
  module cache mount, no shell/dpkg — the deployment surface is one
  compose service + two postgres ones.
- **Cactus never enters a container.** It needs host Metal/CPU kernels;
  it runs on the Mac, and `host.docker.internal` carries the traffic.
  Packaging for "soon": the brew tap formula (`install.sh` does this) —
  the license attribution (MIT) and Gemma model terms are charted in
  the README.
- **Nobody bind-mounts binaries**: configs are a read-only
  `../configs:/configs` mount; the SSH host key is a named volume
  (stable across rebuilds so clients' `known_hosts` stay valid).
