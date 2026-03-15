bb() {
  build-brief "$@"
}

bbg() {
  build-brief -- "$@"
}

# Examples:
#   bb test
#   bb build
#   bbg --stacktrace test
