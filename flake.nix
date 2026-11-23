{
  description = "Nomos - Deterministic engineering substrate and 42-gate AST verification layer between coding agents and Git";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      hashes = builtins.fromJSON (builtins.readFile ./packaging/nix/hashes.json);
      version = hashes.version;
      platformAssets = {
        "x86_64-linux" = {
          asset = "nomos_Linux_x86_64.tar.gz";
          sha256 = hashes."x86_64-linux";
        };
        "aarch64-linux" = {
          asset = "nomos_Linux_arm64.tar.gz";
          sha256 = hashes."aarch64-linux";
        };
        "x86_64-darwin" = {
          asset = "nomos_Darwin_x86_64.tar.gz";
          sha256 = hashes."x86_64-darwin";
        };
        "aarch64-darwin" = {
          asset = "nomos_Darwin_arm64.tar.gz";
          sha256 = hashes."aarch64-darwin";
        };
      };
      supportedSystems = builtins.attrNames platformAssets;
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          spec = platformAssets.${system};
        in
        {
          default = pkgs.stdenvNoCC.mkDerivation {
            pname = "nomos";
            inherit version;
            src = pkgs.fetchurl {
              url = "https://github.com/mgantlett/nomos/releases/download/v${version}/${spec.asset}";
              sha256 = spec.sha256;
            };
            sourceRoot = ".";
            dontConfigure = true;
            dontBuild = true;
            installPhase = ''
              runHook preInstall
              install -Dm755 nomos "$out/bin/nomos"
              install -Dm644 LICENSE "$out/share/licenses/nomos/LICENSE"
              install -Dm644 NOTICE "$out/share/licenses/nomos/NOTICE"
              runHook postInstall
            '';
            meta = {
              description = "Deterministic engineering substrate and 42-gate AST verification layer between coding agents and Git";
              homepage = "https://github.com/mgantlett/nomos";
              license = pkgs.lib.licenses.asl20;
              mainProgram = "nomos";
              platforms = supportedSystems;
            };
          };
        });

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/nomos";
        };
      });
    };
}
