bb() {
  build-brief "$@"
}

bbj() {
  build-brief --mode json "$@"
}

# Examples:
#   bb test
#   bb -- --stacktrace test
#   bbj build
