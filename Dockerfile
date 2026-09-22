# Build a static binary and ship it in a distroless image.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /burp ./cmd/burp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /burp /burp
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/burp"]
CMD ["serve"]
