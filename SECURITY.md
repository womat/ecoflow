# Security

`ecoflowd` runs in a home network with the credentials of an EcoFlow account and can switch tasks of a
PowerOcean (`--block`). With `--listen` it opens an HTTPS server on the addresses given (TLS 1.3, never on
every interface): the web page at `/` is public and carries no data, `/status` and `/block` need the token
`ECOFLOWD_HTTP_TOKEN`. `/status` shows the readings, the connection state and the addresses of `/block`
callers. Reports of security issues are taken seriously.

## Reporting a vulnerability

Please **do not open a public issue**. Report it privately through GitHub instead:
**Security → Report a vulnerability** ([direct link](https://github.com/womat/ecoflow/security/advisories/new)).

Helpful details:

- the affected version (`ecoflowd --version`)
- steps to reproduce
- what an attacker could achieve with it

Never post your EcoFlow password, access or secret keys, the `ECOFLOWD_HTTP_TOKEN`, the MQTT password,
certificates or the serial numbers of your devices in an issue, a discussion or a log excerpt — mask them
(e.g. `HC31XXXXXXXXXXXX`).

You will usually get an answer within a week. This is a spare-time project, so no fixed response time can be
promised.

## Supported versions

Security fixes are made for the latest release only.
