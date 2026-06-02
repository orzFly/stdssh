{
  inputs = {
    dev.url = "path:./.nix";
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.follows = "dev/nixpkgs";
  };

  outputs = inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      perSystem = { system, pkgs, config, ... }:
        let
          common = import ./.nix/common.nix { inherit pkgs; };
          version = "git-${inputs.self.shortRev or inputs.self.dirtyShortRev or "unknown"}";
          src = pkgs.lib.fileset.toSource {
            root = ./.;
            fileset = pkgs.lib.fileset.unions [
              ./go.mod
              ./go.sum
              ./cmd
              ./internal
            ];
          };
          stdssh = pkgs.buildGoModule {
            pname = "stdssh";
            inherit version src;
            go = common.go;
            subPackages = [ "cmd/stdssh" ];
            vendorHash = "sha256-1AOwmr6QQHMBQD2tPbfJfUM66jnkkwqxx9km7pSSNwc=";
            doCheck = false;
            # Match the goreleaser release build: static (CGO off) + stripped.
            # -trimpath is already added by buildGoModule by default.
            env.CGO_ENABLED = 0;
            ldflags = [ "-s" "-w" "-X main.version=${version}" ];
          };
        in
        {
          packages.stdssh = stdssh;
          packages.default = config.packages.stdssh;
        };
    };
}
