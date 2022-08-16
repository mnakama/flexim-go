with import <nixpkgs> {};
stdenv.mkDerivation {
  name = "flexim";
  buildInputs = [ pkg-config go gtk3 ];
  buildPhase = ''
    NIX_CFLAGS_COMPILE="$(pkg-config --cflags gtk+-3.0) $NIX_CFLAGS_COMPILE"
    make
  '';
}
