{
  description = "beadle development shell";

  inputs = {
    nixpkgs.url = "nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];

      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      devShells = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            name = "beadle-dev";

            packages = with pkgs; [
              bashInteractive
              coreutils
              curl
              direnv
              fd
              gh
              git
              gnumake
              go_1_26
              go-tools # staticcheck
              gopls
              jq
              markdownlint-cli2
              nodejs_24
              ripgrep
              shellcheck
            ];

            shellHook = ''
              export BEADLE_NIX_SHELL=1
              export MARKDOWNLINT=markdownlint-cli2
              echo "beadle dev shell: Go $(${pkgs.go_1_26}/bin/go version), Node $(${pkgs.nodejs_24}/bin/node --version)"
            '';
          };
        });
    };
}
