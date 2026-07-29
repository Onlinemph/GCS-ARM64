# GCS Online — feasibility notes and roadmap

Goal: read and (eventually) edit GURPS character sheets from a phone or
tablet, without installing a desktop app.

This document records what was actually tested rather than what seems likely,
so the parts still unproven are easy to tell apart from the parts that work.

## The short answer

Yes, and the expensive part is already done for us.

GCS's rules engine — point costs, skill levels, weapon damage, encumbrance,
prerequisites, bonuses — lives in `model/`, and it builds and runs headless on
Linux. `web/` in this repo is a working server that proves it: it loads a
`.gcs` file, runs the full recalculation, and renders a mobile-shaped page.
No GCS source is forked or patched.

What is *not* solved is editing. See phase 2 onward.

## What was verified

On Linux/amd64 with Go 1.26 and `GOEXPERIMENT=jsonv2`, against GCS at the
version pinned in `web/go.mod`:

| Question | Result |
|---|---|
| Does `model/...` compile without a GUI? | Yes, once X11/GL **headers** are installed |
| Does a server binary link and run with no display? | Yes — no `DISPLAY`, no X server, no window |
| Does loading a sheet produce correct derived values? | Yes — skill levels, weapon damage, Basic Lift, encumbrance all match the desktop app |
| Can output be shaped for a phone? | Yes — upstream's export-template pipeline renders arbitrary HTML |
| How coupled is `model/` to the GUI toolkit? | 19 of 250 files, all of it theming (colors, fonts, key bindings) |

The last row is the important one. `unison` — the Skia/OpenGL toolkit GCS
draws with — is a *compile-time* dependency of the model layer, not a runtime
one. The server binary links `libGL` and `libX11` and never calls into them.

### Consequences for deployment

The binary needs those shared libraries present, so a runtime container needs
`libgl1 libx11-6 libfontconfig1 libfreetype6` even though it renders nothing.
`web/Dockerfile` does this. Nothing needs a GPU, an X server, or a display.

## Options considered

**Stream the desktop app** (run GCS in a container, ship pixels over
WebRTC/VNC). Rejected. It works, but a desktop UI driven through a phone
screen is exactly the experience we are trying to avoid, and it costs a
container per concurrent user.

**Compile the model to WebAssembly** and run entirely in the browser — an
offline PWA, sheets never leaving the device. Very attractive, and blocked
today: `GOOS=js` cannot compile `unison`'s cgo. It becomes possible only after
the model layer's theming dependency is severed (see phase 4).

**Server holds the model, browser holds the UI.** Chosen. It works right now,
it reuses the rules engine as-is, and it keeps upstream compatibility free —
tracking a new GCS release is a `go get`.

## Phases

### Phase 0 — spike (done, in `web/`)

A Go server that serves `.gcs` files as mobile pages: tabbed sections
(Stats / Combat / Traits / Skills / Spells / Gear / Notes), touch-sized
controls, dark and light themes, per-section filtering, and HP/FP trackers
held on the device. Read-only.

### Phase 1 — make it pleasant to actually use at a table

- Rolling: tap a skill or weapon to roll against it, with the modifier stack
  visible. This is what a phone is genuinely better at than paper.
- Offline: service worker plus a web app manifest, so an open sheet survives a
  basement with no signal.
- Multiple sheets per party, switchable without going back to the index.

None of this needs write access to the sheet, which keeps it cheap.

### Phase 2 — a real API

The current `/api/sheet/{id}` serves the raw `.gcs` document, which is already
JSON but holds only *stored* values — not the computed ones. The computed
sheet lives in an unexported `exportedEntity` in `model/gurps/export.go`,
reachable today only by rendering a template.

Two ways out, in order of preference:

1. Upstream a writer-based export API — `ExportTo(w io.Writer, ...)` plus an
   exported view-model type. This is a small, self-contained change that also
   removes this project's per-request temp file, and it benefits any other
   tool embedding GCS. Worth proposing before writing our own.
2. Failing that, a local mapping layer from the public `*gurps.Entity` API.
   Straightforward, but it duplicates logic upstream already maintains and
   will drift.

### Phase 3 — editing

The hard phase, and the one to design carefully rather than grow into.

- `Entity.Save` already exists, so writing back is not the problem.
- Concurrency is: two players on one sheet, or one player on two devices, will
  clobber each other without a conflict story. Sheets are documents, so
  last-writer-wins is the wrong default.
- Sensible boundary: edit through operations (spend points, buy equipment,
  adjust an attribute) rather than by PUTing a whole document.
- Storage stops being "a directory of files" here, and identity/auth becomes
  unavoidable if sheets outlive a session.

### Phase 4 — offline-first, optional

Sever the model layer's `unison` dependency — 19 files, all theming — behind a
build tag or a small interface. That unlocks `GOOS=js` and with it a real PWA:
sheets stay on the device, no server, no account. It also makes the model
layer independently useful, which is a decent argument for doing it upstream
rather than in a fork.

Worth doing only if offline and privacy turn out to matter more than sharing.

## Known rough edges in the phase 0 spike

- **Temp file per render.** `gurps.Export` takes template and output *paths*,
  not writers, so each request writes and reads a scratch file. Correct but
  wasteful; fixed properly by phase 2 option 1.
- **Markdown in note fields** is rendered by a deliberately small client-side
  subset (bold, italic, `---`, `- ` bullets). Upstream uses goldmark, which an
  export template cannot reach. Anything more elaborate belongs in phase 2.
- **No authentication.** Anyone who can reach the port can read every sheet in
  the library directory. Fine on a LAN, not fine on the open internet.
- **Uploads are trusted after parsing.** They are size-limited, rejected
  unless the model can load them, and deleted after six hours, but a hostile
  `.gcs` file is only as contained as upstream's parser.
