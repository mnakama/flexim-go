with import <nixpkgs> {};
buildGoModule {
  name = "flexim";
  nativeBuildInputs = [ pkg-config ];
  buildInputs = [ gtk3 ];

  src = builtins.filterSource
    (path: type: baseNameOf path != ".git"
                 && baseNameOf path != "default.nix"
                 && baseNameOf path != "result")
    ./.;

  vendorHash = "sha256-n8Z0RbOXyT0yLECM5ogRVFSqNJbbsmsWJilT2ByN3DA=";

  buildPhase = ''
    ls -l
    make
  '';

  installPhase = ''
    mkdir -p $out
    cp flexim-{chat,listener,client} {irc,discord}-client $out/
  '';
}
