
## 2026-10-10 - Task: 无感部署 kedaya 合并
- 线上切到 `local/sub2api:kedaya-211-merge2-20261010`。nginx 回到 127.0.0.1:18080，18085 已释放。
- 只执行加列和建表：232、234、239、245、264。239 改写 0 行。未执行 243、255、263、271。
- 验证：容器 healthy，18080 /health 200，https /health 200，登录 400，models 401，前端版本 2.0.65。
- 回滚镜像 `local/sub2api:scheduler-hotfix-20261010`。备份含 compose、nginx 和 build_context，戳为 kedaya-211-merge2-20261010-204647。
- 未提交。
