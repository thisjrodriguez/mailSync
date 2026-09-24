# mailsync

**English** · [Español](README.es.md)

Copy mail from one IMAP server to another. It does not forward: it connects to
both mailboxes, downloads each message raw and drops it into the destination
with `APPEND`.

This sidesteps the problem with SMTP forwarding, which hosting providers block
and Gmail rejects over SPF/DKIM/DMARC: there is no new delivery here, only a
copy.

- The source is never modified: it is read with `BODY.PEEK`, so nothing gets marked as read.
- Message bytes are untouched — never parsed, never rewritten.
- The original date and flags (`\Seen`, `\Answered`, `\Flagged`, `\Draft`) are preserved.
- One-way and append-only: deleting at the destination never touches the source.
- Idempotent: run it a thousand times without duplicating anything.

## Install

    go build -o mailsync .

You get a single binary with no runtime dependencies.

## Usage

    mailsync init      create the working directory and an example config.yaml
    mailsync folders   list the folders in each account's source mailbox
    mailsync check     validate the config and test the login, copying nothing
    mailsync run       copy in a loop, at each account's interval
    mailsync once      make a single pass and exit (handy for cron)
    mailsync status    show what has been synced so far
    mailsync fingerprint HOST[:PORT]
                       show the certificate a server presents, so you can pin it

Always start with `check`: most problems are credentials, ports or TLS, and this
shows them in two seconds instead of letting you find out through a silent
failure at three in the morning.

Then run `folders` to see the real mailbox names before writing your folder
list — guessing them is how migrations end up half-copied.

## Working directory

Everything lives together, so backing up one folder covers all of it:

    ~/.config/mailsync/
      config.yaml     the only file you edit
      secrets.env     optional: your passwords, kept out of the config
      state.db        SQLite holding each folder's position
      mailsync.log

The path is resolved in this order: `--config`, `$MAILSYNC_HOME`,
`$XDG_CONFIG_HOME/mailsync`, `~/.config/mailsync`.

The directory is `0700` and the credential files `0600`. This is checked on
every start, not just at creation: if the permissions are wider, mailsync
refuses to run.

## Configuration

> **Would rather not write the YAML by hand?** Use the
> [configuration generator](https://thisjrodriguez.github.io/mailSync/): fill in a form
> and it hands you the `config.yaml`. It runs entirely in your browser, sends
> nothing anywhere and never asks for passwords.

```yaml
accounts:
  - name: work

    source:
      host: mail.mydomain.com
      port: 993                    # default
      user: user@mydomain.com
      password: ${WORK_PASS}
      tls: tls                     # "tls" (993) or "starttls" (143)

    dest:
      type: gmail                  # fills in imap.gmail.com:993
      user: youraccount@gmail.com
      password: ${GMAIL_APP_PASS}

    folders:
      - INBOX                      # same name at the destination
      - from: INBOX.Sent           # or renamed
        to: mydomain/Sent

    interval: 5m
```

### Where to put passwords

Three ways, and you can mix them across accounts:

**In a separate file.** Put `${WORK_PASS}` in the config and the value in
`secrets.env` — or `.env`, both names are accepted — in the same directory:

```
WORK_PASS=the-password
GMAIL_APP_PASS=abcd efgh ijkl mnop
```

The `config.yaml` then holds no secrets, so you can share or version it.

**In an environment variable** of the same name. It takes precedence over the
file, which is what makes this convenient for containers and systemd.

**Nowhere at all.** Drop the `password` line and mailsync asks for it at
startup, without echoing it as you type:

```
Contraseña de user@mydomain.com (work, origen):
```

It is asked once, before the first connection, and reused for the life of the
process. This is the safest option because the password never touches disk, but
it cannot work under `cron` or for a service that starts on its own: with no
terminal, mailsync says so instead of starting half-configured.

### Log size

The log is capped and rotates on its own, so a daemon you never look at cannot
fill the disk. Defaults to 5 MB per file plus three rotated copies — about
20 MB at worst. To change it:

```yaml
log:
  max_size: 5MB     # 500KB, 5MB, 1GB, or a plain byte count
  keep: 3           # rotated copies; 0 keeps none
```

Structure goes in the YAML; secrets do not have to. Any `${VARIABLE}` is
substituted from the environment or from `secrets.env`, a file of `KEY=value`
lines in the same directory. The environment wins. Substitution happens on
values, not on text: a `${...}` written inside a comment is left alone.

## Servers with an invalid certificate

Shared hosting often serves a self-signed certificate under the node's own name,
which normal TLS verification rejects. Pin it instead of disabling verification:

    mailsync fingerprint mail.mydomain.com:993

Check that the certificate is the one you expect, then add the digest to that
endpoint:

```yaml
    source:
      host: mail.mydomain.com
      fingerprint: 3fa9c1e7b0d24856af73c9e1082b64d5ff17ae3c95b0d8427e6a1cb35d940f2e
```

With a pin, mailsync requires an exact match against that certificate. An
impostor is still rejected — which is what skipping verification would not do.
If the server legitimately changes its certificate, the connection fails and
tells you to re-run `fingerprint`.

## Gmail as the destination

With an app password, no OAuth:

1. Turn on 2-step verification for your Google account.
2. Google Account → Security → App passwords. Generate one.
3. Paste it into the config; the spaces are stripped for you.
4. Gmail → Settings → Forwarding and POP/IMAP → enable IMAP.

Gmail has labels rather than folders. A `to: mydomain/Sent` creates the
corresponding nested label.

## How duplicates are avoided

For each folder, mailsync stores the mailbox's `UIDVALIDITY` and the last UID
copied. Each pass asks only for what is above that mark.

If the source server changes its `UIDVALIDITY` — renumbering the mailbox and
rendering the stored UIDs meaningless — mailsync walks the folder again from
scratch, but filters out by `Message-ID` the messages it had already copied.
That is why you do not end up with a duplicated mailbox after a hosting
migration.

The progress mark is written after the `APPEND` succeeded: if the process dies
midway, at worst one message is copied twice, never lost.

## Known limits

- No OAuth: username and password only (or an app password).
- Interval polling, not IMAP IDLE. Latency is at most the `interval`.
- Messages larger than 60 MB are skipped and noted in the log.
- Passwords sit on disk in the clear, protected only by file permissions.

## Tests

    go test ./...

The suite starts real in-memory IMAP servers over TLS and exercises the full
cycle: that bytes arrive intact, that the source is not modified, that repeated
passes do not duplicate, that a UID reset breaks nothing, and that a pinned
certificate is enforced.
