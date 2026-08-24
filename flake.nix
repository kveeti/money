{
  description = "Money tracker";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      patchedGo =
        pkgs:
        pkgs.go.overrideAttrs (_: {
          version = "1.26.6";
          src = pkgs.fetchurl {
            url = "https://go.dev/dl/go1.26.6.src.tar.gz";
            hash = "sha256-oHIcVMaIkBRI13rZs+x+p8R0cwdV/4kTgukuy5P/LLE=";
          };
        });
    in
    {
      packages = forAllSystems (
        pkgs:
        let
          frontend = pkgs.stdenv.mkDerivation (finalAttrs: {
            pname = "money-frontend";
            version = "0.0.1";
            src = ./frontend;

            pnpmDeps = pkgs.fetchPnpmDeps {
              inherit (finalAttrs) pname version src;
              pnpm = pkgs.pnpm_10;
              fetcherVersion = 3;
              hash = "sha256-raCssv83dQJozzDLporugZTqDyw6abJEkEgLfuGwx9s=";
            };

            nativeBuildInputs = [
              pkgs.nodejs_24
              pkgs.pnpm_10
              pkgs.pnpmConfigHook
            ];

            buildPhase = ''
              runHook preBuild
              pnpm run build
              runHook postBuild
            '';

            installPhase = ''
              runHook preInstall
              mkdir -p $out
              cp -r ../backend/webdist/. $out/
              runHook postInstall
            '';
          });

          appSource = pkgs.runCommand "money-source" { } ''
            cp -r ${./backend} $out
            chmod -R u+w $out
            rm -rf $out/webdist
            cp -r ${frontend} $out/webdist
          '';

          app = (pkgs.buildGoModule.override { go = patchedGo pkgs; }) {
            pname = "money";
            version = "0.0.1";
            src = appSource;
            vendorHash = "sha256-12To+yaerrq78QhiOsfEFpdgna4VU9cW0Irj4kNJM6k=";
            subPackages = [ "." ];
            doCheck = false;

            ldflags = [
              "-s"
              "-w"
            ];

            postInstall = ''
              mv $out/bin/backend $out/bin/money
            '';

            meta.mainProgram = "money";
          };
        in
        {
          default = app;
        }
      );

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            (patchedGo pkgs)

            pkgs.postgresql_18
            pkgs.openssl

            pkgs.nodejs_24
            pkgs.pnpm_10
            pkgs.playwright-driver.browsers
          ];

          postgresConf = pkgs.writeText "postgresql.conf" ''
            # Add Custom Settings
            log_min_messages = warning
            log_min_error_statement = error
            log_min_duration_statement = 100
            log_connections = on
            log_disconnections = on
            log_duration = on
            log_timezone = 'UTC'
            log_statement = 'all'
            log_directory = 'pg_log'
            log_filename = 'postgresql-%Y-%m-%d_%H%M%S.log'
            logging_collector = on
            log_min_error_statement = error
          '';

          shellHook = ''
            export PLAYWRIGHT_BROWSERS_PATH="${pkgs.playwright-driver.browsers}"
            for chromium in "${pkgs.playwright-driver.browsers}"/chromium-*/chrome-linux64/chrome; do
              if [ -x "$chromium" ]; then
                export PLAYWRIGHT_CHROMIUM_EXECUTABLE="$chromium"
                break
              fi
            done

            export PORT=8000
            export DEVIDP_PORT=5557

            if [ -f .env ]; then
              set -a
              source .env
              set +a
            fi

            free_port() {
              local port="$1"
              shift
              while (echo >/dev/tcp/localhost/"$port") 2>/dev/null || [[ " $* " == *" $port "* ]]; do
                port=$((port + 1))
              done
              echo "$port"
            }

            export PGDATA="$PWD/.pg"
            export PORT="''${PORT:-8000}"
            export DEVIDP_PORT="$(free_port "''${DEVIDP_PORT:-5557}" "$PORT")"
            export VITE_PORT="$(free_port "''${VITE_PORT:-3000}" "$PORT" "$DEVIDP_PORT")"

            if pg_ctl -D "$PGDATA" status >/dev/null 2>&1; then
              export PGPORT="$(awk 'NR == 4 { print; exit }' "$PGDATA/postmaster.pid")"
            else
              echo "Setting up ${pkgs.postgresql_18.name}"
              export PGPORT="$(free_port "''${PGPORT:-5556}" "$PORT" "$VITE_PORT" "$DEVIDP_PORT")"

              if [ ! -f "$PGDATA/PG_VERSION" ]; then
                echo "Initializing database..."
                initdb -D "$PGDATA" -U postgres
                cat "$postgresConf" >> "$PGDATA/postgresql.conf"
              fi

              pg_ctl -D "$PGDATA" -o "-k $PGDATA" start
            fi

            export PGHOST="$PGDATA"
            export DEV_VITE_URL="http://localhost:$VITE_PORT"
            export DB_URL="postgres://postgres:postgres@localhost:$PGPORT/postgres"

            if command -v dev-url >/dev/null 2>&1; then
              dev-url money-8000 "$PORT" >/dev/null
              dev-url money-oidc "$DEVIDP_PORT" >/dev/null
              export BACKEND_URL="https://money-8000.dev-internal.veetik.com"
              export DEVIDP_ISSUER="https://money-oidc.dev-internal.veetik.com"
              export VITE_HMR_CLIENT_PORT=443
            else
              export BACKEND_URL="http://localhost:$PORT"
              export DEVIDP_ISSUER="http://localhost:$DEVIDP_PORT"
            fi

            export OIDC_CLIENT_ID=dev
            export OIDC_CLIENT_SECRET=dev

            echo "Ports: backend $PORT, frontend $VITE_PORT, IdP $DEVIDP_PORT, Postgres $PGPORT"
            echo "URLs: app $BACKEND_URL, IdP $DEVIDP_ISSUER"

            alias fin="pg_ctl -D $PGDATA stop && exit"
            alias pg="psql -U postgres -d postgres"
          '';
        };
      });

      nixosModules.default =
        { pkgs, ... }@args:
        let
          moneyPkg = self.packages.${pkgs.system}.default;
        in
        import ./module.nix { inherit moneyPkg; } args;
    };
}
