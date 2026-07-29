# gcsweb — GCS character sheets on a phone

A small Go server that reads GURPS Character Sheet (`.gcs`) files and serves
them as mobile-shaped web pages.

It is a *thin wrapper*, not a reimplementation. Sheets are loaded and fully
recalculated by the upstream [GCS](https://github.com/richardwilkes/gcs)
model packages and rendered through upstream's own export-template pipeline.
Nothing is forked or patched; tracking a new GCS release is a `go get`.

Status: read-only spike. See [../docs/gcs-online-plan.md](../docs/gcs-online-plan.md)
for what works, what does not, and where this is going.

## Running it

```sh
go build -o gcsweb .
./gcsweb -library ~/GCS/Characters
```

Then open `http://<your-machine>:8080` on your phone, on the same network.

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8080` | Listen address |
| `-library` | `samples` | Directory scanned for `.gcs` files |
| `-max-upload` | `8388608` | Maximum upload size in bytes |

Sheets can also be uploaded from the browser. Uploads are held in a scratch
directory and deleted after six hours; the library directory is only ever
read.

### Build requirements

Go 1.26+ with `GOEXPERIMENT=jsonv2` (upstream GCS requires it), and the X11
and OpenGL **development headers**:

```sh
sudo apt-get install libgl-dev libx11-dev libxcursor-dev libxrandr-dev \
                     libxinerama-dev libxi-dev libxkbcommon-dev pkg-config
```

The server never opens a window and needs no display — but GCS's model layer
imports the `unison` GUI toolkit for its theme types, so the toolkit's cgo
still has to compile and link. At runtime the corresponding shared libraries
must be present; see the Dockerfile.

## Docker

```sh
docker build -t gcsweb .
docker run -p 8080:8080 -v ~/GCS/Characters:/sheets:ro gcsweb -library /sheets
```

## Routes

| Route | Purpose |
|---|---|
| `GET /` | Library index and upload form |
| `POST /upload` | Accept a `.gcs` upload |
| `GET /sheet/{id}` | The mobile sheet |
| `GET /api/sheet/{id}` | The raw `.gcs` document (stored values only, not computed ones) |
| `GET /healthz` | Liveness probe |

## Layout

```
main.go              flags, HTTP server lifecycle
server.go            sheet lookup, upload store, render pipeline
handlers.go          route handlers
pages/index.html     the library index (a normal Go template)
templates/           GCS export templates — upstream's format, our layout
```

`templates/sheet.html.tmpl` is where the mobile sheet lives. It is an
upstream "GCS HTML Template v1" file: the first line is the format marker,
everything after it is `html/template` with the exported character as its
data. Changing the sheet layout means editing that one file.

## Not yet

No editing, no authentication, no rolling. Anyone who can reach the port can
read every sheet in the library directory — run it on a LAN, not on the open
internet. The roadmap covers each of these.
