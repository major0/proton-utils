"""Run golangci-lint against only the lines a change actually touches.

The baseline revision is the crux of this hook. pre-commit exports
PRE_COMMIT_FROM_REF when it is invoked with --from-ref, which CI does with the
pull request base, so in CI the baseline is the merge target and a pull request
is judged solely on its own diff. Run from a git hook the variable is unset and
HEAD is the correct baseline, because the developer is linting uncommitted work.

The upstream dnephin hook hard-codes --new-from-rev HEAD. That is correct
locally but wrong in CI, where the checkout already sits at the pull request
head with a clean tree: HEAD diffs against itself, the baseline collapses, and
pre-existing findings anywhere in the module are attributed to whichever pull
request happens to touch a Go file.

This is Python rather than shell because pre-commit requires Python, so it is
guaranteed present, and because it runs unchanged on Windows, where a POSIX
shell may not exist. This repository builds for windows/amd64 and windows/arm64,
so contributors may well be developing there.

golangci-lint still analyses the whole module, which it must do for type
information. Only the reporting is narrowed.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys

LINTER = "golangci-lint"
INSTALL_HINT = ".github/scripts/install-go-tools.sh"


def die(message: str) -> None:
    """Print an error to stderr and exit non-zero."""
    print(f"error: {message}", file=sys.stderr)
    raise SystemExit(1)


def baseline_revision() -> str:
    """Return the revision golangci-lint should treat as already reviewed.

    PRE_COMMIT_FROM_REF is set only when pre-commit runs with --from-ref, which
    is the CI invocation. Falling back to HEAD gives the right local behaviour.
    """
    return os.environ.get("PRE_COMMIT_FROM_REF") or "HEAD"


def require_linter() -> str:
    """Return the linter executable path, or exit if it is not installed.

    shutil.which resolves the .exe suffix on Windows, so no platform special
    casing is needed here.
    """
    path = shutil.which(LINTER)
    if path is None:
        die(f"{LINTER} not found on PATH; run {INSTALL_HINT}")
    return path


def require_revision(revision: str) -> None:
    """Exit unless the revision resolves in this repository.

    Failing loudly matters: a shallow clone cannot resolve the pull request
    base, and silently degrading to a whole-module lint is exactly the
    behaviour this hook exists to prevent.
    """
    completed = subprocess.run(
        ["git", "rev-parse", "--verify", "--quiet", revision],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    if completed.returncode != 0:
        die(
            f"baseline revision {revision} not found; "
            "a shallow clone cannot resolve the pull request base"
        )


def main(argv: list[str]) -> int:
    """Lint the diff against the baseline and propagate the linter exit code."""
    linter = require_linter()
    revision = baseline_revision()
    require_revision(revision)

    # --fix corrects findings in place, which is most useful when run from a git
    # hook. It is safe in CI too: pre-commit fails any hook that modifies files,
    # independently of that hook's exit code, so an auto-fixed finding still
    # surfaces as a failure rather than a silent pass.
    command = [linter, "run", "--new-from-rev", revision, "--fix", *argv]
    return subprocess.run(command, check=False).returncode


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
