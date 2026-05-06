#!/usr/bin/env node
"use strict";

const fs = require("fs");
const os = require("os");
const path = require("path");
const { spawnSync } = require("child_process");

const root = path.resolve(__dirname, "..");
const home = os.homedir();
const binDir = path.join(home, ".local", "bin");
const configDir = path.join(home, ".config", "iSpeak");
const plistPath = path.join(home, "Library", "LaunchAgents", "com.iSpeak.plist");
const socketPath = path.join(configDir, "ispeak.sock");
const binaryPath = path.join(binDir, "ispeakd");
const cliPath = path.join(binDir, "ispeak");
const buildPath = path.join(root, "build", "ispeakd");

function run(cmd, args, options = {}) {
  const result = spawnSync(cmd, args, {
    cwd: root,
    stdio: options.stdio || "inherit",
    encoding: "utf8",
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0 && !options.allowFailure) {
    throw new Error(`${cmd} ${args.join(" ")} failed with exit code ${result.status}`);
  }
  return result;
}

function ensureDir(dir) {
  fs.mkdirSync(dir, { recursive: true });
}

function copyExecutable(src, dst) {
  fs.copyFileSync(src, dst);
  fs.chmodSync(dst, 0o755);
}

function symlinkForce(target, linkPath) {
  try {
    fs.rmSync(linkPath, { force: true });
  } catch (_) {
    // Ignore stale link cleanup failures; copyExecutable below will surface real errors.
  }
  fs.symlinkSync(target, linkPath);
}

function copyIfMissing(src, dst, mode) {
  if (fs.existsSync(dst)) {
    console.log(`配置文件已存在: ${dst}`);
    return;
  }
  fs.copyFileSync(src, dst);
  if (mode) {
    fs.chmodSync(dst, mode);
  }
  console.log(`配置文件已创建: ${dst}`);
}

function sleep(ms) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

function waitForSocket(timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      if (fs.statSync(socketPath).isSocket()) {
        return true;
      }
    } catch (_) {
      // Socket not ready yet.
    }
    sleep(100);
  }
  return false;
}

function main() {
  if (process.platform !== "darwin") {
    throw new Error("@xdfnet/ispeak currently supports macOS only.");
  }

  const goCheck = spawnSync("go", ["version"], { stdio: "ignore" });
  if (goCheck.status !== 0) {
    throw new Error("Go is required to install @xdfnet/ispeak from npm. Install Go first, then rerun npm i -g @xdfnet/ispeak.");
  }

  ensureDir(path.dirname(buildPath));
  ensureDir(binDir);
  ensureDir(configDir);
  ensureDir(path.dirname(plistPath));

  console.log("编译 ispeakd...");
  run("go", ["build", "-ldflags=-s -w", "-o", buildPath, "."]);

  console.log("停止旧服务...");
  run("launchctl", ["unload", plistPath], { allowFailure: true, stdio: "ignore" });

  copyExecutable(buildPath, binaryPath);
  copyExecutable(path.join(root, "scripts", "ispeak"), cliPath);
  symlinkForce(cliPath, path.join(binDir, "ispeak-claude"));
  symlinkForce(cliPath, path.join(binDir, "ispeak-codex"));

  copyIfMissing(path.join(root, "configs", "config.example.json"), path.join(configDir, "config.json"));
  copyIfMissing(path.join(root, "configs", "hook-speak.sh"), path.join(configDir, "hook-speak.sh"), 0o755);

  const plist = fs
    .readFileSync(path.join(root, "configs", "com.iSpeak.plist"), "utf8")
    .replaceAll("BINARY_PATH_PLACEHOLDER", binaryPath);
  fs.writeFileSync(plistPath, plist);

  console.log("启动服务...");
  run("launchctl", ["load", plistPath]);

  if (!waitForSocket(5000)) {
    throw new Error(`service started but socket is not ready: ${socketPath}`);
  }

  const status = run(cliPath, ["status"], { stdio: "pipe" });
  process.stdout.write(status.stdout);
  console.log("\n安装成功！");
}

try {
  main();
} catch (err) {
  console.error(`ispeak npm install failed: ${err.message}`);
  process.exit(1);
}
