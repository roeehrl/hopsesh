# Security policy

hopsesh reads and copies Claude Code transcripts, which can contain secrets, between machines
over SSH. Security reports are very welcome.

## Reporting a vulnerability

Please **do not open a public issue.** Use GitHub's private vulnerability reporting on this
repository (Security → Report a vulnerability). We aim to acknowledge reports within 3 working
days and to ship a fix or mitigation for confirmed high-severity issues within 30 days.

## Scope

In scope: anything that could make hopsesh read or write outside the paths it is allowed to
touch, move credentials, trust a host it shouldn't, leak transcript contents, execute remote
input, or install an unsigned update.

## Supported versions

Only the latest release receives security fixes until 1.0.
