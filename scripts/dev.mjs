// A development launcher only. The two Go commands remain independently usable.
import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
const isWindows = process.platform === 'win32';
const binary = resolve(root, isWindows ? 'bin/blux.exe' : 'bin/blux');
function signalTree(child, signal) {
  if (isWindows) spawnSync('taskkill.exe', ['/PID', String(child.pid), '/T', ...(signal === 'SIGKILL' ? ['/F'] : [])], { stdio: 'ignore' });
  else process.kill(-child.pid, signal);
}
const children = [];
let stopping = false;
let result = 0;

function stop(code) {
  if (stopping) return;
  stopping = true;
  result = code;
  for (const child of children) {
    if (child.pid && child.exitCode === null && child.signalCode === null) {
      try { signalTree(child, 'SIGTERM'); } catch { /* Already exited. */ }
    }
  }
  const timer = setTimeout(() => {
    for (const child of children) {
      if (child.pid && child.exitCode === null && child.signalCode === null) {
        try { signalTree(child, 'SIGKILL'); } catch { /* Already exited. */ }
      }
    }
  }, 6500);
  timer.unref();
  finish();
}

function finish() {
  if (stopping && children.every((child) => !child.pid || child.exitCode !== null || child.signalCode !== null)) {
    process.exit(result);
  }
}

function start(command, args) {
  const child = isWindows && command === 'pnpm'
    ? spawn(process.env.ComSpec || 'cmd.exe', ['/d', '/s', '/c', 'pnpm --dir web dev'], { cwd: root, stdio: 'inherit' })
    : spawn(command, args, { cwd: root, stdio: 'inherit', detached: true });
  children.push(child);
  child.on('error', (error) => {
    console.error(`启动失败：${error.message}`);
    stop(1);
  });
  child.on('exit', (code, signal) => {
    if (!stopping) {
      console.error(`${command} 已退出（${signal ?? code}），停止其他开发进程。`);
      stop(code || 1);
    }
    finish();
  });
  return child;
}

process.on('SIGINT', () => stop(0));
process.on('SIGTERM', () => stop(0));
console.log('\nBlux · 本地 Agent 工作台\n浏览器：http://127.0.0.1:5174\n停止：Ctrl+C\n');
start(binary, ['serve']);
start(binary, ['worker']);
start('pnpm', ['--dir', 'web', 'dev']);
