# Contributing to hana-agents

Thank you for helping. hana-agents is maintained by the hanaML lab; issues and pull requests are welcome.

## Set-up

```bash
git clone https://github.com/hanaml-lab/hana-agents && cd hana-agents
uv sync          # Python packages and dev tools into .venv
make build       # runtimed, agentctl, policyd into bin/
```

You need Go 1.25 or later, Python 3.10 or later with uv, Node 22.18 or later, and Java 11 or later for `make spec`.

## Before you open a pull request

```bash
make lint        # ruff, gofmt
make test        # Go, Python and TypeScript tests, then the Go integration test
make crash-suite # if you touched agent-runtime
make spec        # if you touched the decision table, approvals or the journal's rules
make example     # the browser job end to end
```

- Add a test for every change in behaviour, at the package's public interface: `runtime.Engine`, `decide.Engine`,
  `runtimed`'s HTTP routes, `agent_runtime.Client`, `browser_gym.Env`. Write it first and watch it fail.
- A change to how approvals are spent, how tools are called or how results are journalled must keep
  `go/policy-engine/spec/Approvals.tla` true to the code; add a mutant to `spec/mutants.txt` for any new invariant.
- Add a line under "Unreleased" in `CHANGELOG.md`.
- Keep commits small, with messages in the form the history uses: `type(scope): summary`, where type is one of
  `feat`, `fix`, `test`, `docs`, `refactor`, `ci` or `chore`, and scope is the component (`runtime`, `policy-engine`,
  `browser-gym`, `sdk`, `spec`).

## Reporting a security issue

Please do not open a public issue. Follow the
[hanaML security policy](https://github.com/hanaml-lab/.github/blob/main/SECURITY.md).

## Licence

By contributing you agree that your contribution is licensed under the Apache License 2.0, the licence of this
repository (inbound = outbound).
