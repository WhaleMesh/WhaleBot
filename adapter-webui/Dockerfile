FROM node:20-alpine AS nodebuild
WORKDIR /app
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund
COPY web/ .
RUN npm run build

FROM golang:1.23-alpine AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /adapter-webui ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=nodebuild /app/dist /srv
COPY --from=gobuild /adapter-webui /usr/local/bin/adapter-webui
EXPOSE 18083
ENTRYPOINT ["/usr/local/bin/adapter-webui"]
