FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/chatmodel ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/chatmodel /usr/local/bin/chatmodel
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/chatmodel"]
