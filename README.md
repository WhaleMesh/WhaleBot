# WhaleBot

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go 1.22](https://img.shields.io/badge/Go-1.22-00ADD8.svg)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED.svg)](https://docs.docker.com/compose/)
[![GitHub Stars](https://img.shields.io/github/stars/WhaleMesh/WhaleBot?style=flat)](https://github.com/WhaleMesh/WhaleBot/stargazers)

默认文档语言为中文。English version: [`README.en.md`](README.en.md)。

WhaleBot 是一个以 Docker Compose 为核心的多组件 AI 编排系统：单机即可完整运行；`userdocker` 工作容器还可分布到多台机器（远端节点只需能出站连接编排器，无需公网地址）。  
它的目标不是把所有能力塞进一个进程，而是让各能力作为独立服务协作，并由编排层统一对外提供入口。

本项目**面向开发者**：希望为构建智能体的人提供尽可能高的组合与替换自由。你可以把仓库里自带的服务当作起点，用自建镜像替换或并行扩展其中任意一环，而不必 fork 单体应用后再改天换地。

![](assets/overview.png)

## 设计理念与架构原则

- **容器即组件**：每种能力运行在独立容器中，向编排层注册后接入整体；组件可独立部署、独立健康检查。
- **热插拔与可替换**：默认 compose 提供一套可运行的初始组件；你随时可以接入自己构建的 tool 容器、适配器或模型网关，扩展智能体能力而不重写核心编排。
- **能力如何生长**：需要新能力时，实现对应功能的 tool（或环境）容器，按注册约定接入框架后，`runtime` 即可发现并调用。实现细节见 `orchestrator/`、`runtime/` 及各模块 README。
- **与 Docker 的协同**：智能体可通过现有路径（例如 `user-docker-manager`）创建与管理动态容器；在架构上也可以延伸到「由智能体自建 tool、自我扩展能力」等场景，但接口与生命周期规范仍在完善中，后续会以 schema 与文档形式固化。
- **统一入口**：日常使用主要面对 `orchestrator` 与 `webui`。
- **先可用再扩展**：即便缺少部分外部凭据（例如模型 key、Telegram token），栈仍尽可能保持可启动、可联调。

## 范式定位

当前仓库提供的是一套**可运行的参考范式**：它演示了组件如何协作、如何接到最小可用的对话闭环。  
范式本身**不是**智能体能力的上限——上限取决于你接入的组件生态与规范，而不是把更多逻辑塞进单个进程。

## 系统框架（整体视图）

```mermaid
flowchart LR
  user["User"] --> webui["webui"]
  user --> adapterWebui["adapter-webui"]
  tg["Telegram"] --> adapterTelegram["adapter-telegram"]
  webui --> orchestrator["orchestrator"]
  adapterWebui --> orchestrator
  adapterTelegram --> orchestrator
  orchestrator --> runtime["runtime"]
  orchestrator --> session["session"]
  orchestrator --> llmOpenai["llm-openai"]
  orchestrator --> toolDocker["user-docker-manager"]
  orchestrator --> logger["logger"]
  orchestrator --> skills["skills"]
  orchestrator --> memory["memory"]
  orchestrator --> workspace["workspace"]
  orchestrator --> stats["stats"]
```

## 快速开始

完成环境启动并在 WebUI 中配好 **LLM** 与 **Telegram Bot Token** 后，即可在 Telegram 客户端里直接与你的 Bot 私聊（消息经 `adapter-telegram` 进入编排与 `runtime`）。

1. **准备环境变量**

根目录 `.env` 主要承载 Compose 与通用变量；**不必**在根文件里配置模型 API 密钥（模型侧在 `llm-openai` 数据卷 / WebUI 中配置，见 `AGENTS.md`）。

```bash
cp .env.example .env
```

按需编辑 `.env`；多数场景可先保留示例默认值。

2. **启动系统**

```bash
docker compose up -d --build
```

3. **打开 WebUI 并登录**

浏览器访问 `http://localhost:18000`，按界面完成**初始账号**（凭据在 `webui` 数据卷中持久化）。

4. **创建 Telegram Bot（若还没有）**

在 Telegram 中打开 [@BotFather](https://t.me/BotFather)，发送 `/newbot`，按提示设置显示名与用户名；创建成功后 BotFather 会给出 **HTTP API token**，复制备用。更细的说明见 [Telegram Bots 介绍](https://core.telegram.org/bots#6-botfather)。

5. **在 WebUI 中配置 LLM 与 Telegram**

- **LLM**：在 **LLM** 页配置上游地址、API 密钥与模型等（写入 `llm-openai` 侧，例如默认 `LLM_CONFIG_PATH` JSON）。无有效密钥时仅适合本地占位 / echo 联调，无法支撑真实对话质量。
- **Telegram**：在 **适配器** 页为 `adapter-telegram` 填入上一步的 **bot token**；可按需设置用户 ID 白名单。**未填 token** 时服务仍会注册，但不会长轮询，Telegram 侧收不到消息。

两者均配置有效后，`adapter-telegram` 开始轮询，即可在 Telegram 里搜索你的 Bot 并发送消息进行对话。

不想用 Telegram 也可以直接使用内置的 **Web 聊天界面**（`adapter-webui`）：浏览器访问 `http://localhost:18083`，默认账号与 WebUI 相同。

6. **API 入口（可选）**

编排层 HTTP：`http://localhost:18080`

7. **扩展 userdocker 节点（可选，分布式）**

任何能出站访问编排器的机器都可以成为 userdocker 节点：`user-docker-manager` 主动拨号 `POST /api/v1/nodes/connect`（凭 `NODE_TOKEN`），随后通过该反向隧道对编排器提供完整 API，机器本身无需公网地址。容器统一以 `"<节点>/<容器名>"` 复合名寻址。

```bash
# 在远端机器上（先改好 NODE_TOKEN，与主机 .env 保持一致）
ORCHESTRATOR_PUBLIC_URL=http://your-hub-host:18080 \
NODE_NAME=gpu-box-1 NODE_TOKEN=<shared-token> \
docker compose -f docker-compose.node.yml up -d --build
```

**关于当前内置示例**：仓库内置**两个**用户侧适配器（Telegram 与 Web 聊天）与**一条** OpenAI 兼容的 LLM 路径（`llm-openai`），构成可运行的对话闭环；WebUI 另提供**模型基准测试**（Benchmark）页，便于在多个本地模型间横向比较。更多适配器与模型后端将随组件 **schema** 与 AGENT 文档完善后更易扩展（适配器进度反馈模式见 `docs/adapter-progress-pattern.md`）。

## Roadmap / 近期方向

- 完善各类组件的接口 schema 定义，并配套 AGENT 文档，使开发者能低成本编写或生成新组件。
- 完善 Docker 生命周期管理，加强编排与 Docker 的交互能力，使智能体与容器的协作更顺滑。
- 优化提示词与 ReAct 工作流。
- 增加更多常用 adapter。
- 更多细项以各模块 TODO 与 Issue 为准。

## 仓库结构

以下为高层说明；各目录的实现细节见对应子模块 README。

- `orchestrator/`：编排与网关（组件注册/心跳、节点隧道、API 反向代理）
- `runtime/`：ReAct 执行循环（含模型基准测试 harness）
- `session/`：会话持久化（SQLite，支持空闲过期）
- `skills/`：技能库（文件系统技能包 + SQLite FTS5 检索索引）；对外经 orchestrator 暴露 `/api/v1/skills*` 等路由
- `llm-openai/`：OpenAI 兼容模型调用适配（多 profile，WebUI 管理）
- `adapter-telegram/`：Telegram 用户 I/O 适配器
- `adapter-webui/`：ChatGPT 风格 Web 聊天适配器（宿主端口 18083）
- `user-docker-manager/`：`user docker` 系统管理（列举、创建、移除、重启、接口发现）；每台节点机器一个实例，经出站反向隧道接入编排器
- `logger/`：日志服务（事件持久化）
- `stats/`：Overview 统计服务（可选，停用即禁用指标）
- `memory/`：记忆服务（KV 笔记 + AES-256-GCM 加密的密钥库，agent 经 `{{secret:key}}` 占位符引用）
- `workspace/`：工作区服务
- `userdocker-base/`：动态 userdocker 基础镜像
- `whalebot/userdocker-golang:latest`：动态 userdocker 的 Go 工具链镜像变体（由 `userdocker-base` 构建流程产出）
- `webui/`：管理面板前端（Svelte + Caddy，含登录鉴权）

## 文档与信息优先级

当信息不一致时，按以下顺序判断：

1. `docker-compose.yml`（运行事实）
2. `.env.example`（配置事实）
3. `AGENTS.md`（面向 AI agent 的低 token 项目快照）
4. 根 `README.md` 与各模块 `README.md`（说明文档）

## 贡献说明

提交贡献前，请同步检查并更新 `AGENTS.md`。  
只要你的改动影响了架构、服务清单、端口、环境变量、运行方式或项目状态，就必须在同一提交中更新 `AGENTS.md`。
