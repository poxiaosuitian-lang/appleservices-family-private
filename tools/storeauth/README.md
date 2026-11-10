# Store authentication helper

This isolated module pins `majd/ipatool` to
`v2.8.1-0.20261109170727-e0e2908dc866`. It authenticates only: it has no purchase
command. The parent must enforce an overall process deadline because upstream
`Login` has no context argument.

Build with `./build.ps1`; the executable defaults to the project's
`bin/storeauth.exe` (override with `-OutputPath`). The script loads
`D:/appleservices-deps/environment.ps1` (override with `-EnvironmentFile`) for Go
dependency and build caches, and enables automatic toolchain selection. Add
`-Prepare` to download and validate public runtime assets without authenticating.
Preparation and the parent use `APPLESERVICES_CACHE_DIR` for `LOCALAPPDATA` and
`XDG_CACHE_HOME`; the build script defaults this runtime cache to
`D:/appleservices-deps/runtime-cache`, matching `run-ui.ps1`. The script restores
its caller's environment and working directory.

The parent communicates using private stdin/stdout pipes and newline-delimited
JSON, version 1. Credentials must never be command arguments, environment
variables, log messages or temporary files. The child uses direct network
transport and removes proxy environment variables within its own process.

The first message has type `login` with `email`, `password`, `mac_address`, and
`expected_dsid`. Optional `state` is base64-encoded `anisette-v4` data from the
same account and hardware identity. Only after a `challenge` response may the
parent obtain a fresh trusted-device code and send a `code` message. There is at
most one challenge. A final `result` has `ok`, and on success contains `email`,
`dsid`, `token`, `storefront`, `pod`, `guid`, cookie setting events and optional
`state`. Cookie events preserve original attributes and must be replayed in order.

The parent must validate DSID, email, GUID, cookie origins and result bounds
again before adopting the session. Keep the resulting session and state in
memory. A successful login does not establish that an iCloud+ purchase succeeded;
the parent must still verify the subscription and current offer before submission.

Failures return only an allowlisted `storeauth-*` stage and optional HTTP status.
They never include upstream error strings, response metadata or account objects.
Input and output are each limited to 1 MiB, state to 512 KiB, and cookie settings
to 512 cookies. Keychain operations and cookie persistence are memory-only.

Run unit tests from this directory with `go test ./...`; they do not contact
Apple or load public runtime assets.
