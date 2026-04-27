ARG FINAL_BASE_IMAGE=alpine:3.20
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/userdocker ./

FROM ${FINAL_BASE_IMAGE}
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/userdocker /usr/local/bin/userdocker
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/userdocker"]
