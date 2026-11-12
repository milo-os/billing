# Builds billing's staff-portal plugin (Vite + Module Federation) and serves
# the built output as static files with a dependency-free Node server
# (serve.ts).
#
# Internal-only: no Gateway fronts this service. staff-portal's server is the
# only caller, over the in-cluster Service DNS name, and proxies the assets to
# the browser. TLS is still required because
# ServiceConfiguration.spec.userInterface.provider.assets.baseURL must be an
# https:// URL; the cert comes from a cert-manager CSI volume mounted at
# /etc/tls (config/components/provider-ui-plugin/deployment.yaml).
#
# Node and pnpm versions must match package.json's engines/packageManager.
# Node 25+ no longer ships corepack, so pnpm is installed at its pinned version.

FROM node:26.13.1-slim AS build
WORKDIR /app
RUN npm install --global pnpm@12.16.1
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml .npmrc ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm run build

FROM node:26.13.1-slim
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY serve.ts ./
# uid 1000, matching the Deployment's runAsUser.
USER node
EXPOSE 8443
CMD ["node", "serve.ts"]
