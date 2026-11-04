# Builds billing's staff-portal plugin (Vite + Module Federation) and serves
# the built output as static files with Bun's own server (serve.ts).
#
# Internal-only: no Gateway fronts this service. staff-portal's server is the
# only caller, over the in-cluster Service DNS name, and proxies the assets to
# the browser. TLS is still required because
# ServiceConfiguration.spec.userInterface.provider.assets.baseURL must be an
# https:// URL; the cert comes from a cert-manager CSI volume mounted at
# /etc/tls (config/components/provider-ui-plugin/deployment.yaml).

FROM oven/bun:1 AS build
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . .
RUN bun run build

FROM oven/bun:1-slim
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY serve.ts ./
USER bun
EXPOSE 8443
CMD ["bun", "run", "serve.ts"]
