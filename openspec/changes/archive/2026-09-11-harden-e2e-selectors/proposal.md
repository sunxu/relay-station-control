## Why

现有 E2E 大多使用 role/label，但少数测试仍依赖 Ant Design class、表单 DOM 层级、`.first()` 或页面文案。Runtime Acceptance 已暴露这类 selector 在页面结构或文案调整后容易产生误报或无意义阻塞。

## What Changes

建立 selector inventory，并在后续实现阶段为稳定 UI 身份和业务实体补充最小 `data-testid` / `data-*` 契约。第一阶段只定义清单和边界，不修改 UI 或 E2E。

## Non-goals

- 不改变 UI、API、可访问性行为或业务逻辑。
- 不修改 production data model、OpenAPI 或 acceptance harness。
- 不构建大型 page-object/E2E framework。

## Status

Planning only. This change currently contains inventory and proposed minimal selector additions; implementation is deferred pending review.
