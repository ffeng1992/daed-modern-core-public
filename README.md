# daed Modern Core Bridge

这是一个 daed v2.1.1 管理界面与后端适配 DAE v2.1.1 核心的集成项目。它不是 daed 或 DAE 官方发行版。

官方 daed v2.1.1 发布包使用较早的 DAE 核心提交；本项目保留原定的 DAE v2.1.1 提交及必要补丁，并对新版管理接口做适配。版本与差异见[升级记录](docs/UPSTREAM-V2.1.1.md)。

## 项目状态

请先看[部署说明](docs/DEPLOYMENT.md)和[兼容性清单](compatibility/versions.json)。本项目当前提供 Debian x86_64 新装流程；它不会迁移已有 daed 数据，也不声称适配所有内核、网络拓扑、机场或负载。

## 一键部署

在全新 Linux/Debian `x86_64` 主机上，以 root（通常使用 `sudo`）运行。宿主还需要 Git、Python 3、iproute2（`ss`）、已运行且 root 可访问的 Docker Engine daemon，以及 Docker Compose v2。克隆子模块和构建镜像需要出站网络。项目没有声明最低 Debian 或内核版本；内核/BPF 适用性需由管理员核实。宿主条件、冲突检查范围和失败后的处理见[完整部署说明](docs/DEPLOYMENT.md)。

```sh
git clone --recurse-submodules https://github.com/ffeng1992/daed-modern-core-public.git
cd daed-modern-core-public
sudo ./deploy.sh
```

部署脚本会校验固定的 DAE 源码与补丁，并检查指定的 daed/dae 服务与单元、同名容器、TCP 2023 监听和 `/etc/daed` 中的现有数据；这不是通用主机冲突扫描。它不会覆盖或迁移现有安装。成功提示只表示管理页面 HTTP 健康检查通过。管理员还需完成页面初始化、节点/订阅、DNS 和路由配置，并独立验收真实代理与直连流量。部署使用特权容器与主机网络，请先阅读[完整部署说明](docs/DEPLOYMENT.md)。

## 设计约束

尽量保持与两个上游项目的差异小而清楚：旧管理界面中不再适用的设置由适配层忽略；新核心功能另行设计管理页面。更新 daed、DAE 或两者时，必须按更新清单重新检查接口、配置语义、生命周期和流量行为。不能保证未来版本无需适配即可兼容。

## 上游来源与许可证

本项目包含或适配了 [daed](https://github.com/daeuniverse/daed) 与 [DAE](https://github.com/daeuniverse/dae) 的代码。各部分的版权声明、许可证和修改说明以仓库中的许可证文件及源码声明为准；这些上游材料不会被本项目的名称或说明替代。

## 使用边界

该部署路径仅用于全新安装，不是原版 daed 的升级包。管理页面应限制在可信管理网络中，不要直接暴露到公网。源码和补丁保持可检查；上游版本变化时必须重新验证接口、配置语义、生命周期和流量行为。
