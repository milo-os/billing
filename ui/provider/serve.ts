/**
 * Minimal static file server for the built plugin bundle (dist/), with TLS.
 * Internal-only: no Gateway fronts this service, so the only client is
 * staff-portal's own server (see Dockerfile for why TLS is required
 * regardless). Cert and key come from the cert-manager CSI volume mounted by
 * the Deployment, which renews them in place well before they expire, so the
 * server re-reads them every minute and switches to a new pair when it changes.
 *
 * Plain Node, no dependencies: Node 26 runs this TypeScript directly (type
 * stripping), so keep it to erasable syntax (no enums or namespaces).
 */
import { readFileSync } from 'node:fs';
import { readFile, stat } from 'node:fs/promises';
import { createServer } from 'node:https';
import { extname, join, normalize, sep } from 'node:path';

const DIST_DIR = join(import.meta.dirname, 'dist');
const TLS_DIR = process.env.TLS_DIR ?? '/etc/tls';
const PORT = Number(process.env.PORT ?? 8443);
const TLS_RELOAD_MS = Number(process.env.TLS_RELOAD_MS ?? 60_000);

const CONTENT_TYPES: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.map': 'application/json; charset=utf-8',
  '.ts': 'text/plain; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
  '.zip': 'application/zip',
};

/** The file under dist/ for a request path, or undefined if it would leave dist/. */
function resolvePath(pathname: string): string | undefined {
  let decoded: string;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return undefined;
  }
  const file = normalize(join(DIST_DIR, decoded));
  return file === DIST_DIR || file.startsWith(DIST_DIR + sep) ? file : undefined;
}

function readTLS(): { cert: Buffer; key: Buffer } {
  return {
    cert: readFileSync(join(TLS_DIR, 'tls.crt')),
    key: readFileSync(join(TLS_DIR, 'tls.key')),
  };
}

let tls = readTLS();

const server = createServer(
  tls,
  async (req, res) => {
    if (req.method !== 'GET' && req.method !== 'HEAD') {
      res.writeHead(405, { Allow: 'GET, HEAD' }).end();
      return;
    }
    const file = resolvePath(new URL(req.url ?? '/', 'https://localhost').pathname);
    try {
      if (!file || !(await stat(file)).isFile()) throw new Error('not a file');
      const body = await readFile(file);
      res.writeHead(200, {
        'Content-Type': CONTENT_TYPES[extname(file)] ?? 'application/octet-stream',
        'Content-Length': body.length,
      });
      res.end(req.method === 'HEAD' ? undefined : body);
    } catch {
      res.writeHead(404, { 'Content-Type': 'text/plain' }).end('Not Found');
    }
  }
);

// Pick up renewed certificates. A read can land between the driver writing the
// new cert and the new key; setSecureContext then throws on the mismatched pair
// and the next tick retries, while the server keeps serving the old pair.
const reloadTimer = setInterval(() => {
  try {
    const next = readTLS();
    if (next.cert.equals(tls.cert) && next.key.equals(tls.key)) return;
    server.setSecureContext(next);
    tls = next;
    console.log('Reloaded TLS certificate');
  } catch (err) {
    console.error('TLS reload failed, keeping the current certificate:', err);
  }
}, TLS_RELOAD_MS);
reloadTimer.unref();

server.listen(PORT, () => console.log(`Serving ${DIST_DIR} on https://0.0.0.0:${PORT}`));

// Let Kubernetes stop the pod promptly: finish in-flight requests, then exit.
process.on('SIGTERM', () => server.close(() => process.exit(0)));
