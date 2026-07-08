# Security Policy

## Supported versions

Only the latest released version is actively maintained. Security fixes are issued against `main` and tagged with the next patch release.

| Version | Supported |
|---------|:---------:|
| Latest release | ✅ |
| Older releases | ❌ please upgrade |

The latest release is the most recent `v*` tag on https://github.com/amcheste/kagents/releases.

## Reporting a vulnerability

**Please do not open a public issue, Discussion, or pull request for security vulnerabilities.** Use GitHub's [private vulnerability reporting](https://github.com/amcheste/kagents/security/advisories/new) instead. That surface lets you submit confidentially, and the maintainer can collaborate with you on a fix without the report being visible to anyone else until it's resolved.

Please include in your report:

- A clear description of the vulnerability
- Steps to reproduce (or a proof-of-concept manifest / kubectl invocation)
- The kagents version you observed it on
- Potential impact. What an attacker could achieve, and against what cluster topology

## Coordinated disclosure expectations

We follow a **coordinated disclosure** process:

1. **Acknowledgement**. Within **7 days** of your report, the maintainer will confirm receipt and start triage.
2. **Triage + fix**. Within **30 days**, you will receive either a fix candidate, a status update with a clear timeline, or a written explanation of why the report doesn't qualify as a vulnerability.
3. **Embargo**. Fix development happens in private. We ask you to keep the issue confidential until the fix ships and is publicly announced. We will not embargo for longer than 90 days from the original report without your agreement.
4. **Public disclosure**. Once the fix is released, we publish a [GitHub Security Advisory](https://github.com/amcheste/kagents/security/advisories) with the details, affected versions, mitigation steps, and credit to you (unless you ask to remain anonymous).
5. **CVE assignment**. If the issue qualifies, we request a CVE through GitHub's CNA before public disclosure.

## What counts as a security issue

If you're not sure whether something is a vulnerability or a bug, err on the side of reporting it through the private channel. It's easy to move a non-security report to a public issue, but a public report of a real vulnerability is unfixable damage.

In-scope examples:

- Privilege escalation between agent pods within a team or across teams
- Container escape from an agent pod to the node
- Reading secrets that an agent's RBAC scope shouldn't allow
- Operator manipulating arbitrary cluster resources beyond its declared RBAC
- Information disclosure through dashboard endpoints or webhook payloads
- Supply-chain weaknesses in the operator or runner container images

Out of scope (please file as regular GitHub issues):

- Bugs without a security impact
- Denial-of-service requiring cluster-admin access to set up
- Issues that require physical access to a node
- Best-practice deviations that don't enable an attack

## Hardening checklist for operators

For users deploying kagents in production, the [Operations explanation](https://kagents.dev/explanation/operations/) covers the defense-in-depth model. Per-agent ServiceAccounts, the file-based-protocol threat model, and what RBAC does and doesn't enforce. Reading that page before going live is recommended.
