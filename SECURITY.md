# Security policy

rcplug runs as root on the device and can change the desk's state, so security reports
are taken seriously.

## Reporting a vulnerability

Please **do not** open a public issue. Use GitHub's private vulnerability reporting
(the repository's *Security* tab → *Report a vulnerability*). Include what an attacker
needs (network access, a browser visit, …), what they can achieve, and how to
reproduce it. You will get an answer within two weeks. If private reporting is not
available, open an issue that only asks for a private contact, without any details.

## Supported versions

Only the latest release receives fixes.

## Scope

In scope: the host, the console and the device installer in this repository, for
example ways
to change the desk from another web page, to bypass the control level, the deny list or
the PIN, to read secrets from the device, or to run code on the device through the web
interface.

Known limits that are not vulnerabilities: there are no user accounts, and without a
PIN anyone on the local network can use the web interface over plain HTTP (see
[docs/safety.md](docs/safety.md)). The device's own security (SSH access, firmware) is
RØDE's responsibility, not rcplug's. The setup and firmware tools and the other plugins
have their own repositories (rcplug-toolkit, rcplug-plugins), with the same policy.
