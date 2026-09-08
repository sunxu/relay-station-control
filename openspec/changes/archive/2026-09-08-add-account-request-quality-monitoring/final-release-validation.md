# Final archive gate

日期：2026-09-08。Implementation Final Review：APPROVED。

归档前HEAD为`7304d68`（fix(acceptance): stabilize recovery and cancellation gates），main工作树干净，任务12/12。

本次在该HEAD重新执行：

- `make test build`：PASS，Web 16文件/74测试，Go测试与构建通过。日志`/private/tmp/request-quality-archive-build.log`。宿主Go模块cache有权限warning，命令退出0；未覆盖GOCACHE/GOTMPDIR。
- README隔离PostgreSQL55432环境：`go test -race ./internal/store -run '^TestAccountRequestQuality' -count=1 -v`：5项PASS、无SKIP，9.112s。日志`/private/tmp/request-quality-archive-pg.log`。
- `go test -race ./internal/requestquality ./internal/drivers/cliproxyapi ./cmd/control -run 'TestNormalize|TestCollector|TestUsageQueue|TestCurrentIdentities|TestAccountRequestQuality' -count=1`：全部PASS。日志`/private/tmp/request-quality-archive-race.log`。
- `openspec validate add-account-request-quality-monitoring --type change --strict --no-interactive`：PASS。
- `openspec validate --all --strict`：17 passed，0 failed。
- `git diff --check`：PASS。

100000合成事件：15m查询58.07ms，1h查询53.07ms；分别返回99800/100000条，p95为951/950ms。实际边界随DB时间推进；正确性由独立窗口fixture验证。

冻结边界复核：CLIProxy/CPA没有本次修改；Node原有AGENTS.md工作树修改未触碰。PostgreSQL only，HTTP usage queue为唯一事件source，auth-files仅用于当前identity lookup。resolved/unresolved、NULL账号隔离及Node/Provider统计语义不变；不新增功能。destructive-pop/no-ACK与不可重放窗口保留在spec和`docs/runbooks/account-request-quality.md`。

用户已授权标准归档、同步canonical spec及push main；不部署。
