# Memory roadmap (WhaleBot)

## TODO

1. **轻量级长期记忆 + 自动注入**：用户可编辑的持久槽位（借鉴 [nanobot memory](https://github.com/HKUDS/nanobot/blob/main/docs/memory.md) 的 USER / 项目事实分层）、`runtime` 工具读写、可选回合前从 memory 服务预取注入 system。
2. **RAG 式长期记忆**：文档切块、向量索引与检索工具（或独立服务），与 KV 类记忆互补。

## Note

`memory` 服务源码保留在本目录，但**默认不**随根目录 `docker compose up` 启动；实施上述项时需将服务重新加入 `docker-compose.yml` 或单独运行该容器。
