BINARY  := pvecli
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

# Les paquets dont la couverture est un critère de recette, pas une statistique.
# Ce sont les deux qui portent le contrat : le client de l'API et le pipeline de
# mutation. `cmd` est volontairement hors du seuil — il est surtout fait d'aide
# et de mise en forme, et le compter ferait monter la moyenne sans rien garantir
# de plus.
COVER_PKGS := ./internal/pve ./internal/service
COVER_MIN  := 70

# Le nœud du lab (PVX-055). Surchargeable : make install-node NODE=…
NODE     ?= 192.0.2.23
NODEUSER ?= root
NODEPATH ?= /usr/local/bin/$(BINARY)

# Installation sur le POSTE. `install-node`, juste au-dessus, vise le nœud PVE :
# les deux ne sont pas la même cible et n'installent pas la même chose — le nœud
# ne fait pas tourner Claude Code, il n'y reçoit donc pas l'agent.
#
# ~/.local et non /usr/local : le second exige sudo sur macOS, et une cible
# d'installation qui réclame les droits root pour poser un binaire d'utilisateur
# est une cible qu'on finit par lancer en sudo sans réfléchir.
#   make install PREFIX=/usr/local   reste possible, avec sudo.
PREFIX ?= $(HOME)/.local

.PHONY: build test lint fmt cover integration release install install-node uninstall clean capture help

build: ## Compile le binaire avec la version injectée au build
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

test: ## Lance les tests unitaires (le tag `integration` reste hors du défaut)
	go test ./...

lint: ## go vet + golangci-lint s'il est installé
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint absent — go vet seul (brew install golangci-lint)"; \
	fi

fmt: ## Formate le code
	go fmt ./...

cover: ## Couverture, avec un seuil qui fait échouer la cible
	@fail=0; \
	for pkg in $(COVER_PKGS); do \
		out=$$(mktemp); \
		go test -coverprofile=$$out $$pkg >/dev/null || exit 1; \
		pct=$$(go tool cover -func=$$out | awk '/^total:/ {print substr($$3, 1, length($$3)-1)}'); \
		rm -f $$out; \
		if awk "BEGIN {exit !($$pct < $(COVER_MIN))}"; then \
			printf '%-24s %6s %%  < %s %%  ECHEC\n' $$pkg $$pct $(COVER_MIN); fail=1; \
		else \
			printf '%-24s %6s %%  ok\n' $$pkg $$pct; \
		fi; \
	done; \
	if [ $$fail -ne 0 ]; then \
		echo; \
		echo "Couverture insuffisante. Ce seuil n'est pas une métrique de confort :"; \
		echo "internal/pve et internal/service sont les deux paquets où une"; \
		echo "régression se paie contre un vrai nœud, pas dans un test."; \
		exit 1; \
	fi

integration: ## Tests d'intégration contre un VRAI nœud — VMID 900-999 uniquement
	@test -n "$$PVE_API_URL" || { \
		echo "PVE_API_URL n'est pas défini — les tests d'intégration parlent à un vrai nœud."; \
		echo "  source ~/.config/pvecli/env"; \
		exit 2; }
	go test -tags integration -count=1 -v ./...

release: ## Binaires darwin/arm64 et linux/amd64, statiques, avec leurs sommes
	@test "$(VERSION)" != "dev" || { \
		echo "refus de publier une version « dev » — make release VERSION=v0.1.0"; exit 2; }
	@rm -rf dist && mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 \
		go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$(VERSION)_darwin_arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$(VERSION)_linux_amd64 .
	@cd dist && shasum -a 256 * > SHA256SUMS && cat SHA256SUMS

install-node: ## Copie le binaire linux sur le nœud et vérifie qu'il s'exécute
	@test -f dist/$(BINARY)_$(VERSION)_linux_amd64 || { \
		echo "binaire absent — lance d'abord : make release VERSION=$(VERSION)"; exit 2; }
	scp dist/$(BINARY)_$(VERSION)_linux_amd64 $(NODEUSER)@$(NODE):$(NODEPATH).new
	ssh $(NODEUSER)@$(NODE) 'chmod 0755 $(NODEPATH).new && mv $(NODEPATH).new $(NODEPATH)'
	@echo "--- vérification depuis le nœud ---"
	ssh $(NODEUSER)@$(NODE) '$(NODEPATH) --version'

install: build ## Installe pvecli sur le POSTE, et avec lui l'agent IA dans ~/.claude
	install -d $(PREFIX)/bin
	install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)
	@echo "--- agent Claude Code ---"
	@$(PREFIX)/bin/$(BINARY) ai install || { \
		echo "l'agent n'a pas pu être installé — « pvecli ai install --force » après relecture"; \
		exit 1; }
	@echo "--- vérification ---"
	$(PREFIX)/bin/$(BINARY) --version

uninstall: ## Retire le binaire du poste (l'agent reste : il a pu être personnalisé)
	rm -f $(PREFIX)/bin/$(BINARY)
	@echo "agent conservé — supprime-le à la main si tu le veux :"
	@echo "  rm -f ~/.claude/agents/proxmox-ops.md"

clean: ## Supprime le binaire et les artefacts de publication
	rm -rf dist $(BINARY)

capture: ## Capture une réponse réelle dans testdata/ — make capture ENDPOINT=/nodes
	@test -n "$(ENDPOINT)" || { echo "usage: make capture ENDPOINT=/nodes [NAME=nodes]"; exit 2; }
	@./scripts/capture.sh "$(ENDPOINT)" "$(NAME)"

help: ## Liste les cibles
	@grep -E '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "} {printf "  %-14s %s\n", $$1, $$2}'
