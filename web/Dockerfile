# Build stage. GCS's model layer imports the unison GUI toolkit for its theme
# types, so the toolkit's cgo has to compile even though nothing is drawn —
# hence the X11/GL development headers.
FROM golang:1.26-bookworm AS build

RUN apt-get update && apt-get install -y --no-install-recommends \
        libgl-dev libx11-dev libxcursor-dev libxrandr-dev \
        libxinerama-dev libxi-dev libxkbcommon-dev pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN GOEXPERIMENT=jsonv2 go mod download

COPY . .
RUN GOEXPERIMENT=jsonv2 CGO_ENABLED=1 go build -trimpath -o /gcsweb .

# Runtime stage. The server never opens a display, but the shared libraries it
# linked against must still be resolvable.
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        libgl1 libx11-6 libfontconfig1 libfreetype6 ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --system --create-home --uid 10001 gcsweb
USER gcsweb

COPY --from=build /gcsweb /usr/local/bin/gcsweb

EXPOSE 8080
ENTRYPOINT ["gcsweb"]
CMD ["-addr", ":8080", "-library", "/sheets"]
