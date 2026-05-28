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
            version = "0.1.0";
            inherit src;
            go = common.go;
            subPackages = [ "cmd/stdssh" ];
            vendorHash = "sha256-TwzV2a69cj/d7DA3Rd4+9B2g45iyak/ReQ+5kReEz6c=";
            doCheck = false;
          };
        in
        {
          packages.stdssh = stdssh;
          packages.default = config.packages.stdssh;
        };
    };
}
