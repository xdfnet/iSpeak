# 豆包/火山引擎资料

这里收纳 iSpeak 依赖的豆包/火山引擎 TTS 资料，和项目自身文档分开。

- [SSE/HTTP Chunked TTS API](http-chunked-sse-unidirectional-v3.md)：当前 `ispeakd` 使用的单向 SSE 流式接口资料。
- [音色复刻 API](tts-voice-clone-api-v3.md)：V3 音色复刻训练、查询、升级与错误码资料。
- [在线音色列表](voice-list.md)：可配置到 `voice_type` 的在线音色列表。

项目默认使用新版 API Key 鉴权，并通过：

```text
https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse
```

调用 SSE 接口。
