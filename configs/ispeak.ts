import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const SOCK = `${process.env.HOME}/.config/iSpeak/ispeak.sock`;

function extractText(messages: any[]): string {
  const last = messages?.filter((m: any) => m.role === "assistant")?.at(-1);
  if (!last) return "";

  const content = last.content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";

  return content
    .filter((b: any) => b.type === "text")
    .map((b: any) => b.text)
    .join("\n");
}

export default function (pi: ExtensionAPI) {
  pi.on("agent_end", async (event) => {
    const text = extractText(event.messages ?? []);
    if (!text) return;

    // 通过 bash 发送到 iSpeak Unix Socket，btoa 避免注入
    const encoded = Buffer.from(`{source:pi}${text}`).toString("base64");
    pi.exec("bash", ["-c", `echo ${encoded} | base64 -d | nc -U -w3 ${SOCK}`]);
  });
}
