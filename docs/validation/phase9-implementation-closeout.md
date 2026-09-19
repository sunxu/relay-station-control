# Phase 9 implementation closeout

日期：2026-09-20  
规划冻结：`e659cc26bb77ab61dd3d8d8c16343530effad9d6`

## 结果

`PHASE9_IMPLEMENTATION_RUN = BLOCKED`

实现范围已完成并通过本地验证，但生产验收不能关闭：生产 compose 需要由发布流程提供并核验 Control、Gateway、Node 的外部不可变镜像制品。当前没有执行 GitHub 写入、推送或发布，因此不把本地实现误报为生产制品验收。

## 阶段证据

| 阶段 | 状态 | 证据 |
| --- | --- | --- |
| 1A | PASS | `ops` `d376833`；移除活动 gateway proxy 路径，Gateway 直连 |
| 1B | PASS | `control` `739321c`；移除 Node artifact identity gate，保留观测字段 |
| 1C | PASS | `control` `7021bf2`、`ops` `d2a9e44`；移除过时 rollout gate，加入生产 compose |
| 2A | PASS（外部制品待补） | `ops` `d2a9e44`；服务、secret-init、control-init、健康依赖和 digest 约束已落地 |
| 2B | PASS | `control` `26ce585`；`relay-control-init` 执行 Goose、角色准备、环境/driver/provider seed |
| 2C | PASS | `control` `26ce585`；Node source 未修改，数据库 migration 文件未新增 |
| 2D | PASS | `ops` `d2a9e44`、`231d3bf`；生产/开发部署文档与直连路径一致 |
| 2E | PASS（边界验证） | secret repeat/invalid smoke、compose contract、Control 全量构建测试 |
| 3A | BLOCKED | 未获得当前候选 Control/Gateway/Node 的已发布 GHCR 制品身份，未启动生产整套服务 |
| 3B | BLOCKED | 可审计记录已提交，但外部制品和真实生产运行证据仍缺失 |

## 验证记录

- `control`: `make test build` 通过；Go 测试、web 286 tests、typecheck、build 均通过。
- `ops/dev`: `python3 -m unittest discover -s dev -p 'test_*.py'` 通过，64 tests，15 个历史 proxy/recovery 测试按新路径跳过。
- `ops/production`: compose contract 通过；带必需环境变量的 `docker compose ... config --quiet` 通过。
- `control-secret-init.sh`: repeat 保持 secret 字节不变，invalid preprovisioned secret 拒绝，三类控制密钥互异检查已加入。

## 未关闭项

- `EXTERNAL_RELEASE_ARTIFACT_REQUIRED = YES`：需要发布流程提供与当前源码/构建对应的 Control、Gateway、Node 多架构 digest，并重新执行生产启动、健康、初始化幂等和最小业务路径验收。
- 当前生产 compose 中的 Control digest 是已存在的历史只读制品身份；它不能被当作本次新增 `relay-control-init` 的已验证制品。Gateway/Node digest 也未完成 GHCR 远端身份核验。

## 范围与仓库状态

- `ARCHITECTURE_DELTA_DETECTED = NO`
- Node source unchanged；DB schema/migration unchanged。
- GitHub writes/push/releases = NONE。
- 实现涉及 commits：
  - `control`: `739321c`, `7021bf2`, `26ce585`
  - `ops`: `d376833`, `d2a9e44`, `231d3bf`
