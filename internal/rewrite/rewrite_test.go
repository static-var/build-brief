package rewrite

import "testing"

func TestShellCommandLeavesNonGradleCommandsAlone(t *testing.T) {
	command := "ls -la"
	rewritten, changed := ShellCommand(command)
	if changed {
		t.Fatalf("expected no rewrite, got %q", rewritten)
	}
	if rewritten != command {
		t.Fatalf("expected original command, got %q", rewritten)
	}
}

func TestShellCommandRewritesGradleInvocation(t *testing.T) {
	rewritten, changed := ShellCommand("gradle clean test")
	if !changed {
		t.Fatal("expected gradle command to be rewritten")
	}
	if rewritten != "build-brief gradle clean test" {
		t.Fatalf("unexpected rewrite: %q", rewritten)
	}
}

func TestShellCommandRewritesGradleWrapperInvocation(t *testing.T) {
	rewritten, changed := ShellCommand("./gradlew --stacktrace test")
	if !changed {
		t.Fatal("expected gradle wrapper command to be rewritten")
	}
	if rewritten != "build-brief ./gradlew --stacktrace test" {
		t.Fatalf("unexpected rewrite: %q", rewritten)
	}
}

func TestShellCommandRewritesCommandChains(t *testing.T) {
	rewritten, changed := ShellCommand("which gradle && gradle clean")
	if !changed {
		t.Fatal("expected command chain rewrite")
	}
	if rewritten != "command -v build-brief && build-brief gradle clean" {
		t.Fatalf("unexpected rewrite: %q", rewritten)
	}
}

func TestShellCommandPreservesLeadingCommands(t *testing.T) {
	rewritten, changed := ShellCommand("cd smoke && ./gradlew test")
	if !changed {
		t.Fatal("expected rewrite in chained command")
	}
	if rewritten != "cd smoke && build-brief ./gradlew test" {
		t.Fatalf("unexpected rewrite: %q", rewritten)
	}
}

func TestShellCommandPreservesEnvPrefix(t *testing.T) {
	rewritten, changed := ShellCommand("JAVA_HOME=/tmp/jdk gradle test")
	if !changed {
		t.Fatal("expected env-prefixed command rewrite")
	}
	if rewritten != "JAVA_HOME=/tmp/jdk build-brief gradle test" {
		t.Fatalf("unexpected rewrite: %q", rewritten)
	}
}

func TestShellCommandDoesNotRewriteExistingBuildBriefCommand(t *testing.T) {
	command := "build-brief test"
	rewritten, changed := ShellCommand(command)
	if changed {
		t.Fatalf("expected no rewrite, got %q", rewritten)
	}
	if rewritten != command {
		t.Fatalf("expected original command, got %q", rewritten)
	}
}
