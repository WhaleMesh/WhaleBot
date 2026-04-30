FROM node:20-alpine AS nodebuild
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM golang:1.23-alpine AS authbuild
WORKDIR /src
COPY authsrv/go.mod authsrv/go.sum ./
RUN go mod download
COPY authsrv/main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /webui-auth .

FROM caddy:2-alpine
RUN apk add --no-cache wget
COPY --from=nodebuild /app/dist /srv
COPY --from=authbuild /webui-auth /usr/local/bin/webui-auth
COPY Caddyfile /etc/caddy/Caddyfile
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
EXPOSE 80
ENTRYPOINT ["/docker-entrypoint.sh"]
