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
const plistPath = path.join(home, "Library", "LaunchAgents", "com.ispeak.plist");
const legacyPlistPath = path.join(home, "Library", "LaunchAgents", "com.iSpeak.plist");
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

function migrateDefaultEndpoint(configPath) {
  if (!fs.existsSync(configPath)) {
    return;
  }
  const oldEndpoint = "https://openspeech.bytedance.com/api/v3/tts/unidirectional";
  const newEndpoint = "https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse";
  let config;
  try {
    config = JSON.parse(fs.readFileSync(configPath, "utf8"));
  } catch (_) {
    return;
  }
  if (config.endpoint !== oldEndpoint) {
    return;
  }
  fs.copyFileSync(configPath, `${configPath}.bak`);
  config.endpoint = newEndpoint;
  fs.writeFileSync(configPath, `${JSON.stringify(config, null, 2)}\n`);
  console.log(`配置 endpoint 已迁移到 SSE，旧配置备份: ${configPath}.bak`);
}

function installHook(src, dst) {
  if (fs.existsSync(dst)) {
    try {
      if (fs.readFileSync(src, "utf8") !== fs.readFileSync(dst, "utf8")) {
        fs.copyFileSync(dst, `${dst}.bak`);
        console.log(`旧 Hook 已备份: ${dst}.bak`);
      }
    } catch (_) {
      fs.copyFileSync(dst, `${dst}.bak`);
      console.log(`旧 Hook 已备份: ${dst}.bak`);
    }
  }
  copyExecutable(src, dst);
  console.log(`Hook 脚本已安装: ${dst}`);
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
  run("launchctl", ["unload", legacyPlistPath], { allowFailure: true, stdio: "ignore" });
  run("launchctl", ["unload", plistPath], { allowFailure: true, stdio: "ignore" });
  try {
    fs.rmSync(legacyPlistPath, { force: true });
  } catch (_) {
    // Ignore migration cleanup failures.
  }

  copyExecutable(buildPath, binaryPath);
  copyExecutable(path.join(root, "scripts", "ispeak"), cliPath);
  copyExecutable(path.join(root, "scripts", "ispeak-claude"), path.join(binDir, "ispeak-claude"));
  copyExecutable(path.join(root, "scripts", "ispeak-codex"), path.join(binDir, "ispeak-codex"));
  copyExecutable(path.join(root, "scripts", "ispeak-copilot"), path.join(binDir, "ispeak-copilot"));
  copyExecutable(path.join(root, "scripts", "ispeak-pi"), path.join(binDir, "ispeak-pi"));

  const configPath = path.join(configDir, "config.json");
  copyIfMissing(path.join(root, "configs", "config.example.json"), configPath);
  migrateDefaultEndpoint(configPath);
  installHook(path.join(root, "configs", "hook-speak.sh"), path.join(configDir, "hook-speak.sh"));

  const plist = fs
    .readFileSync(path.join(root, "configs", "com.ispeak.plist"), "utf8")
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
