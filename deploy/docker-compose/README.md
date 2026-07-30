# Docker Compose Encrypted-Corpus Rehearsal

This directory runs four local BLOC operators, one read-only `mempool-il`
encrypted-corpus service, Prometheus, and Grafana. It validates container and
artifact contracts only; it is not VM performance evidence.

Prepare a `bloc-cluster-v3` config and secrets, generate and self-check one
`bloc-encrypted-corpus-v1` artifact offline, then bind the config:

```sh
cd bloc-node
go run ./cmd/bloc-node bind-encrypted-corpus \
  --config ../deploy/docker-compose/generated/n4/cluster.json \
  --corpus ../deploy/docker-compose/generated/n4/encrypted-corpus.json \
  --mempool-url http://mempool-il:8080 \
  --remote-eval ../deploy/docker-compose/remote-eval.compose.json
```

Copy `.env.example` to a local ignored environment file and replace every
placeholder with an existing read-only path and immutable image digest. Resolve
the stack without starting containers:

```sh
docker compose --env-file .env.campaign -f compose.yaml config
```

Only after the resolved configuration and artifact identities pass validation:

```sh
docker compose --env-file .env.campaign -f compose.yaml up --no-build
```

Run an exact-prefix diagnostic from `bloc-node/`:

```sh
go run ./cmd/bloc-node eval-remote \
  --config ../deploy/docker-compose/remote-eval.compose.json \
  --experiment-id compose-encrypted-corpus \
  --final-campaign \
  --tx-source mock-encrypted-corpus \
  --mempool-url http://127.0.0.1:18080 \
  --batch-size 8 \
  --warmups 0 \
  --repetitions 1 \
  --deadline 12s \
  --out-dir results/distributed/compose-encrypted-corpus
```

Acceptance requires four healthy, consistent results; exact requested/received
count; matching public, plaintext, encrypted-corpus, and prefix identities; and
no image build, plaintext mount, request-time encryption, or mutable tag. The
legacy `compose.mock-placeholder.yaml` filename is a no-op compatibility
overlay and cannot re-enable the old runtime-encryption path.

Stop with:

```sh
docker compose --env-file .env.campaign -f compose.yaml down
```

Compose charts remain diagnostic and must not be reported as distributed thesis
evidence.
