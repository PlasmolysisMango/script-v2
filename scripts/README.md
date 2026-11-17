# 内置剧本

本目录包含两类可加载剧本：

- `eastern-star/`：《东方之星号游轮事件》结构化测试包（5 人、单幕、42 条线索），数据来自 [MIRAGE](https://github.com/lime728/MIRAGE)，论文 arXiv:2501.01652，原作者为比格沃斯大呆侠。
- `imported/`：29 个完整剧本及素材，来源、适配与资产说明见 `imported/README.md` 和 `imported/import-report.md`。

以上资料由仓库维护者提供；维护者确认已合法取得并同意在本仓库提交与发布。版权归原权利人，如有异议请联系删除。MIRAGE 仓库未声明剧本数据的许可证，使用和再分发前请确认原权利人的要求。

## eastern-star

质量校验：中文人物本及线索、中文 `Truth.json`、`ScriptEnvs/SOO.py` 中的凶手定义和英文版人数/线索数交叉检查。中文和英文属于同一数据集，不能视为独立原始来源；公开的组织者手册未能完整获取，未宣称已独立校对原作。

导入适配：去掉主持人专用备注；枪箱必须先找到张二副房间的钥匙；缺失图片的密码箱改为第二轮搜证解锁。阶段流程由框架编排：阅读、介绍、两轮搜证讨论、投票、复盘。原人物本和真相未删节，适配之处不冒充原作规则。

重新导入：

```sh
go run ./cmd/jbs import mirage /path/to/MIRAGE/dataset/Zh/东方之星号游轮事件 --truth /path/to/MIRAGE/dataset/Zh/Truth.json --culprit 修经理 --out scripts/eastern-star
```
