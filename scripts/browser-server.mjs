// Playwright-only server: a separate temporary database, never the user's data/.
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
const root = fileURLToPath(new URL('../', import.meta.url));
const directory = mkdtempSync(join(tmpdir(), 'coffee-browser-'));
const child = spawn(join(root, '.tools/coffee-api'), ['-listen', `127.0.0.1:${process.env.COFFEE_E2E_PORT ?? '3189'}`, '-db', join(directory, 'coffee.sqlite')], { cwd: root, stdio: 'inherit' });
const cleanup = () => rmSync(directory, { recursive: true, force: true });
child.once('error', error => { console.error(error); cleanup(); process.exit(1); });
child.once('exit', code => { cleanup(); process.exit(code ?? 1); });
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => child.kill(signal));
