## Tasks

- [ ] 1. 按已冻结设计确认 `/static/` 与 `/assets`、API route 的 collision scan。
- [ ] 2. 更新 frontend build configuration 为 `base: "/static/"`。
- [ ] 3. 更新 embedded server routing：strip `/static/`、命中文件服务、miss 返回 404；保持 `/assets` 和 `/assets/` 为 Asset Registry SPA。
- [ ] 4. 增加 static miss、SPA route、API route 和 lazy-chunk regression tests。
- [ ] 5. 执行 production build 并验证 lazy chunks 成功加载。
- [ ] 6. 更新 Web routing 文档与 Phase 6 prerequisite evidence。
- [ ] 7. 记录测试、回滚说明和 clean-worktree evidence。
