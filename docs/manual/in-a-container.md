# In a container

`fadodb serve` runs in a container as it runs on a machine. The image
holds the tool and no other program, so everything in
[Serving a database](serving-a-database.md) holds here as it stands.
This chapter says what a container changes: where the database is
kept, how the server is told to listen, and how it is reached, watched
and stopped.

The story: Tomek runs the machines at Acme. He serves the database of
Acme's invoices from a container, and Olga, who keeps the database, is
its first administrator. A test plays the commands of the chapter
against the image and compares what the tool and the server answer.

What is not here yet is listed at the end.

## The image

A release publishes the image at `ghcr.io/mojohand70/fadodb`, for
`linux/amd64` and `linux/arm64`, under its version and under no other
name: there is no `latest`, so that a server never changes its engine
by itself. The first was v0.6.0, and the last is v0.7.0:

<!-- pulling it -->
```sh
docker pull ghcr.io/mojohand70/fadodb:0.7.0
docker tag ghcr.io/mojohand70/fadodb:0.7.0 fadodb
```

The second command gives the image the short name `fadodb`, which the
commands of this chapter use.

FadoDB is two programs from one code
([the two programs](serving-a-database.md#a-server-with-the-analytical-engine)),
and from the release after v0.7.0 on there is an image of each:

| The image | The program in it |
|---|---|
| `ghcr.io/mojohand70/fadodb:<version>` | `fadodb`, with the analytical engine. This chapter is about it |
| `ghcr.io/mojohand70/fadodb:<version>-core` | `fadodb-core`, the same program in pure Go, without the engine; see [The small image](#the-small-image) |

The images up to v0.7.0 hold the program without the engine, under
the plain name.

The image is also built from a checkout of the repository, and that
one is what the test of this chapter plays:

<!-- building it -->
```sh
docker build -t fadodb .
```

<!-- the image -->
| | |
|---|---|
| What it holds | the tool, `/fadodb`, the C and C++ libraries the analytical engine needs, and the certificates by which a request of an action knows another service; the licences of FadoDB and of DuckDB, which is linked into the tool, and the notices of the code of others it carries, in `/licenses`. No shell and no other program |
| What it runs | the tool: what follows the name of the image is what follows `fadodb` at a terminal. Told nothing, it prints its help |
| Who it runs as | the user `65532`, who is not root |
| Where a database is kept | `/data`. A command starts there, so `acme.fado` is `/data/acme.fado` |
| The port it names | `8080` |

- **The image serves nothing by itself.** A server in a container has
  to listen beyond its container, and a server that does so is told so
  (below). So the image has no server it starts when it is told
  nothing.
- **It is about 118 MB**: the tool is 90 of them, and the rest the
  libraries of its base. The small image is about 17 MB.

## From nothing to a server

The four commands of
[Serving a database](serving-a-database.md#from-nothing-to-a-server),
each in a container, and before them the volume that keeps the
database:

<!-- from nothing to a server -->
```sh
docker volume create acme
docker run --rm -v acme:/data fadodb init acme.fado
docker run --rm -v acme:/data fadodb user add olga --kind person --system administrators --db acme.fado
docker run --rm -v acme:/data fadodb key new olga --label laptop --db acme.fado > olga.key
docker run -d --name acme -v acme:/data -p 127.0.0.1:8080:8080 --stop-timeout 20 fadodb serve acme.fado --listen :8080 --plain-http
```

| On the line | What it says |
|---|---|
| `docker volume create acme` | the volume `acme` keeps the database. It outlives every container, and removing a container removes nothing of the database |
| `--rm -v acme:/data` | the first three commands each run in a container of their own, which is removed when the command ends. What they made is in the volume |
| `> olga.key` | the key is printed once, to standard output, so it lands in a file on Tomek's machine and not in the container. He closes the file to others, `chmod 600 olga.key`, and gives it to Olga |
| `--listen :8080 --plain-http` | told nothing, the server listens on this machine only, and in a container that is the container alone: nobody would reach it. So it is told to listen beyond, and, since it has no certificate, that it may |
| `-p 127.0.0.1:8080:8080` | the machine hands its port 8080 to the server, to callers on the machine only. What the server is sent is in the clear, a key with every request, so the port is not opened to the network: see [Reaching it from other machines](#reaching-it-from-other-machines) |
| `--stop-timeout 20` | how long the server is given to stop: see [Stopping](#stopping) |

The server says where it serves, as on a machine. Tomek reads it with
`docker logs acme`:

<!-- what the server says -->
```
fadodb serve: serving acme.fado at http://[::]:8080
fadodb serve: it keeps a copy of the documents for the questions that read everything, beside the database
fadodb serve: nobody signs in on a page: give the settings the address people reach the server at, url
fadodb serve: its actions may call no host; the settings list the hosts they may call (--settings)
```

The second line is the analytical engine's: the copy is the file
`acme.fado.duckdb`, in the volume beside the database. The server
makes it again when it is lost, so it needs no backup.

The third line is about people. A server that listens beyond its
machine, as one in a container does, shows its pages for
[signing in](signing-in.md) only at the address its settings give,
`url`: `"url": "http://127.0.0.1:8080"` here, the address Tomek's
machine hands on. Olga and Ann call with keys until then.

## Calling it

A caller on the machine reaches the server at `http://127.0.0.1:8080`,
with a key, as it reaches any server:
[Commands from a terminal](commands-from-a-terminal.md) for a person
and a script, [The door for agents](the-door-for-agents.md) for an
agent.

The image is the whole tool, so it is also a caller, for a machine
that has Docker and no `fadodb`. Olga asks the server who it takes her
for:

<!-- olga calls it -->
```sh
export FADODB_KEY=$(cat olga.key)
docker run --rm --network container:acme -e FADODB_KEY fadodb exec whoami
```

<!-- olga calls it, the answer -->
```jsonc
{
  "user": {
    "id": "olga",
    "kind": "person",
    "groups": [],
    "system_groups": [
      "administrators"
    ],
    "disabled": false
  }
}
```

- **`--network container:acme` puts the tool beside the server**, so
  `http://127.0.0.1:8080`, where the tool calls when it is told
  nothing, is the server.
- **`-e FADODB_KEY` hands the key over from the terminal**, by its
  name alone. The key stands on no line, as
  [the tool asks](commands-from-a-terminal.md#the-server-and-the-key).

An agent's program starts the door for agents from the image the same
way. Its settings name `docker` in place of `fadodb`:

<!-- the door from the image -->
```jsonc
{"mcpServers": {"acme": {
  "command": "docker",
  "args": ["run", "-i", "--rm", "-e", "FADODB_URL", "-e", "FADODB_KEY", "fadodb", "mcp"],
  "env": {"FADODB_URL": "https://fado.acme.example", "FADODB_KEY": "fado_…"}}}}
```

`-i` keeps the standard input of the container open, which is where
the door reads.

## Kept running

A server that is to come back after the machine restarts is written
down once, in a file for Docker Compose. This is
[`examples/container/compose.yaml`](../../examples/container/compose.yaml),
for the volume and the database made above, in place of the last of
the five commands:

<!-- the compose file -->
```yaml
# A database served from a container and kept running. Its chapter of
# the manual is docs/manual/in-a-container.md, which makes the volume
# and the database first. Then:
#
#   docker compose -f examples/container/compose.yaml up -d --wait
services:
  fadodb:
    image: fadodb
    command: ["serve", "acme.fado", "--listen", ":8080", "--plain-http"]
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - acme:/data
    healthcheck:
      test: ["CMD", "/fadodb", "health"]
      interval: 30s
      timeout: 6s
      start_period: 10s
      start_interval: 1s
    stop_grace_period: 20s
    restart: unless-stopped
    read_only: true
    tmpfs:
      - /tmp
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true

volumes:
  acme:
    external: true
```

<!-- kept running -->
```sh
docker compose -f examples/container/compose.yaml up -d --wait
```

| In the file | What it says |
|---|---|
| `healthcheck` | Docker asks the server for its health, with the tool, since the image holds no other program: see [Its health](#its-health). `--wait` comes back when the server answers |
| `stop_grace_period: 20s` | how long the server is given to stop: see [Stopping](#stopping) |
| `restart: unless-stopped` | the server is started again when it dies and when the machine restarts, until Tomek stops it |
| `read_only: true`, `tmpfs: /tmp` | the server writes to its volume and nowhere else in the container. `/tmp` is kept in memory, for a large sort |
| `cap_drop: ALL`, `no-new-privileges` | the server needs none of the rights of the machine that a container is given, and cannot gain one |
| `external: true` | the volume is Tomek's and not the compose file's: `docker compose down --volumes`, which removes what such a file made, leaves the database where it is |

## Reaching it from other machines

A key travels with every request, so beyond the machine the requests
are encrypted, as
[the server asks](serving-a-database.md#where-it-listens). In a
container there are the same two ways.

**The server shows a certificate.** Tomek hands it the folder that
holds the certificate, to read only, and opens its port to the
network:

<!-- with a certificate -->
```sh
docker run -d --name acme -v acme:/data -v /etc/acme/tls:/tls:ro -p 8443:8443 --stop-timeout 20 fadodb serve acme.fado --listen :8443 --tls-cert /tls/cert.pem --tls-key /tls/key.pem
```

The server reads the two files as the user `65532`, so that user must
be allowed to read them.

**Something in front encrypts**: a proxy that holds the certificate,
in a container beside the server. The server keeps `--plain-http`, the
proxy reaches it over the network that the two containers share, at
`http://fadodb:8080` in a compose file, and the line that hands a port
of the machine to the server is left out. Only the proxy is reached
from outside.

## The settings and the secrets

[The settings](serving-a-database.md#the-settings) are a file of
Tomek's, and [the secrets](serving-a-database.md#the-secrets) a folder
of his. He hands both to the container, to read only:

<!-- with the settings -->
```sh
docker run -d --name acme -v acme:/data -v /etc/acme/settings.json:/settings.json:ro -v /etc/acme/secrets:/secrets:ro -p 127.0.0.1:8080:8080 --stop-timeout 20 fadodb serve acme.fado --listen :8080 --plain-http --settings /settings.json --secrets-dir /secrets
```

- **Both are read by the user `65532`.** A secret that only Tomek may
  read on the machine is one the server cannot read: the folder of the
  secrets belongs to `65532`, or the container runs as the user it
  belongs to, with `--user`.
- **A secret may be in the environment** in place of a file, as on a
  machine: `-e FADODB_SECRET_BOOKS__TOKEN` hands it over from the
  terminal by its name.
- **Neither is in the image or in the volume.** The volume holds the
  database; what the server may call is decided on the machine.

## A folder of the machine in place of a volume

A volume is kept by Docker. Tomek may keep the database in a folder of
his own, which he names in place of the volume. The folder must belong
to the user the container runs as: he gives it to `65532`, or runs the
container as its owner:

<!-- a folder of the machine -->
```sh
docker run --rm --user 1000:1000 -v /srv/acme:/data fadodb init acme.fado
```

- **The folder is on a disk of the machine.** The engine keeps the
  latest commits beside the database in a way that a network share
  does not carry, so a database is not kept on one.
- **A database is closed to every user but its owner**, as on a
  machine: whoever runs the container as another user is refused by
  the machine, and not by the engine.

## One container holds the database

While it serves, the server
[holds its database alone](serving-a-database.md#one-program-holds-the-database),
and a second container on the same volume is a second program. With
the server running:

<!-- a second container -->
```sh
docker run --rm -v acme:/data fadodb check acme.fado
```

<!-- a second container, the refusal -->
```
fadodb check: the database acme.fado is held by fadodb serve (process 1) since 2026-11-16 14:41:37 UTC [in_use]; a database that is served is reached through its server, with a key; to open it on the machine, stop the program that holds it
```

- **A database is served by one container.** A second server on the
  same volume is refused the same way, so two copies of the container
  are not two servers.
- **`fadodb check`, `fadodb user add` and `fadodb key new` want the
  server stopped**: `docker stop acme`, the command, `docker start
  acme`. Everything else is done through the server, with a key.
- **A backup is a copy made while the server is stopped**, as
  [Running it](running-it.md#a-backup-and-putting-it-back) says of any
  database.

## Stopping

`docker stop acme` tells the server to stop, and the server stops as
[on a machine](serving-a-database.md#stopping): it ends its streams,
lets the commands that run end, for up to ten seconds, and closes the
database.

<!-- stopping -->
```
fadodb serve: stopping
fadodb serve: stopped
```

- **Docker waits ten seconds and then kills the container**, unless it
  is told otherwise. That is as long as the server gives its commands,
  so the server is given twenty: `--stop-timeout 20` on the line, or
  `stop_grace_period: 20s` in a compose file.
- **A server that was killed is a program that died.** What a commit
  was answered is in the volume;
  [Running it](running-it.md#when-the-program-dies) says what such a
  program leaves.

## Its health

`fadodb health` asks a server for
[its health](serving-a-database.md#its-health) and prints the answer.
It sends no key. It is how Docker watches the server, because the
image holds no other program to ask with:

<!-- the health check -->
```sh
docker exec acme /fadodb health
```

<!-- the health check, the answer -->
```jsonc
{"ok":true,"version":"dev"}
```

Whatever watches containers over HTTP itself asks `GET /v1/health` at
the port of the server, and needs no program in the image.

## Another release

An image is named with its version, and a newer one is put in its
place by Tomek: he stops the server, makes a backup, and starts the
container from the newer image. Before version 1.0 a release may
change how a database is laid out. A database that v0.7.0 or a later
release made is brought forward when the newer server opens it, and
the older one no longer opens it afterwards, so the backup is the way
back; one of a release before v0.7.0 is refused:
[Running it](running-it.md#another-release) says what to keep with
each backup.

## The small image

`fadodb-core` is the same program without the analytical engine. Its
image holds the program, the certificates and the licences, and no
other file: no library, since the program needs none.

<!-- building the small image -->
```sh
docker build -t fadodb-core --target core .
```

<!-- the small image -->
| | |
|---|---|
| What it holds | the tool, `/fadodb-core`, and the certificates; FadoDB's licence and the notices of the code of others the tool carries, in `/licenses`. No shell and no other program |
| What it runs | the tool, as the other image does: `docker run --rm fadodb-core version` |
| Who it runs as, where a database is kept, the port it names | as the other image: the user `65532`, `/data`, `8080` |

Everything in this chapter holds for it, with `fadodb-core` where a
line says `fadodb` before a command. It keeps no copy of the
documents, so its server does not say the line about one, and it
answers the questions that read a whole class by the store:
[up to what size that is enough](serving-a-database.md#a-server-with-the-analytical-engine).
It is about 17 MB.

## Not here yet

- **Several databases in one container.** A container serves one, as
  [`fadodb serve` does](serving-a-database.md#not-here-yet).
- **A backup of a database that is served**, without stopping the
  server.
