ifndef IN_NIX_SHELL
ifneq (,$(wildcard ./.env))
	include .env
	export
endif
endif

.PHONY: all docker-build e2e httpsproxy httpsdev httpsdevi
MAKEFLAGS += -j

docker-build:
	@set -eu; \
	arch="$$(docker info --format '{{.Architecture}}')"; \
	case "$$arch" in \
		aarch64|arm64) system=aarch64-linux; platform=linux/arm64 ;; \
		x86_64|amd64) system=x86_64-linux; platform=linux/amd64 ;; \
		*) echo "Unsupported Docker architecture: $$arch" >&2; exit 1 ;; \
	esac; \
	out="$$(mktemp -d)"; \
	trap 'rm -rf "$$out"' EXIT; \
	docker run --rm --platform "$$platform" \
		-v "$$PWD:/src:ro" -v "$$out:/out" -w /src nixos/nix:latest \
		sh -c "nix --extra-experimental-features 'nix-command flakes' build .#packages.$$system.dockerImage --out-link /tmp/money-image && cp /tmp/money-image /out/money.tar.gz"; \
	docker load < "$$out/money.tar.gz"; \
	docker tag money:latest veetik/money:latest

backdev:
	@cd backend && go run .

# Mock OIDC provider for local login (see backend/cmd/devidp). Point OIDC_ISSUER at it.
devidp:
	@cd backend && go run ./cmd/devidp

frontdev:
	@cd frontend && pnpm run dev

dev: backdev frontdev

# Local HTTPS for providers that require a secure callback. Trust
# backend/.certs/localhost-cert.pem after its first run.
httpsproxy:
	@cd backend && go run ./tools/httpsproxy

httpsdev: export BACKEND_URL=https://localhost:8443
httpsdev: export VITE_HMR_CLIENT_PORT=8443
httpsdev: httpsproxy backdev frontdev

httpsdevi: export BACKEND_URL=https://localhost:8443
httpsdevi: export VITE_HMR_CLIENT_PORT=8443
httpsdevi: export OIDC_ISSUER=$(DEVIDP_ISSUER)
httpsdevi: httpsproxy devidp backdev frontdev

devi: export OIDC_ISSUER=$(DEVIDP_ISSUER)
devi: devidp backdev frontdev

build-frontend:
	@cd frontend && pnpm install && pnpm run build

e2e:
	@cd frontend && pnpm run e2e

# Builds the frontend into backend/webdist, then the backend binary with it embedded.
build: build-frontend
	@cd backend && go build -o ../dist/money .

# Generate a Nordea-format test CSV for load-testing the importer (written beside the Makefile).
# Override params: make gencsv N=200000 O=big.csv DUP=0.1
N ?= 200000
O ?= transactions.csv
DUP ?= 0.05
gencsv:
	@cd backend && go run ./tools/gencsv -n $(N) -o $(abspath $(O)) -dup $(DUP)
