# LeadFinder

个人用的 B2B 商家线索工具（适合做 SEO 服务找客户）。输入**行业 + 地点**，从 **Google Maps**（通过 [gosom/google-maps-scraper](https://github.com/gosom/google-maps-scraper)）和 **OpenStreetMap**（Overpass API）拉取商家信息，然后：

- 按域名、电话、坐标+名称去重合并
- 用 [colly](https://github.com/gocolly/colly) 爬商家官网首页和联系页提取邮箱。支持 `mailto:`、正则匹配、`[at]`/`(dot)` 这类混淆写法、Cloudflare `data-cfemail` 解码，优先排同域名邮箱
- 可选：从商家的 Instagram/Facebook/TikTok 主页补抓邮箱。只做匿名公开访问，不登录，**默认关闭**
- 用 [AfterShip/email-verifier](https://github.com/AfterShip/email-verifier) 验证邮箱：语法、MX 记录、一次性邮箱；SMTP 检查可选
- **GMB 机会分**（Google Business Profile 优化机会，越高越值得联系）：只对**有邮箱**的商家计算，每家列出可解释的「可提升点」（中文标签 + 英文开发信话术 + 权重），总分 = 权重之和。没有邮箱的商家单独放进「暂不可触达」列表，不参与主排序
- **增量覆盖格子**：同一「城市 + 行业」共用已扫格子；扩大半径时只扫新圆环，已扫区域不再请求，减轻封 IP 风险；线索库跨任务合并去重
- 结果存 SQLite（纯 Go，无需 CGO）。提供 Web 界面（创建任务、看进度、看结果表、导出 CSV）和命令行

> ⚠️ 本工具**没有发邮件功能**，也不会加。

## 可触达 + GMB 机会分

主列表 = 至少有一个邮箱的商家（优先与官网同域名的邮箱，如 `info@店名.com`，标「✓同域」）。网页任务页有三个标签：**可触达 / 暂不可触达 / 全部**，CSV 也可分别导出（`/jobs/ID/csv`、`?view=unreachable`、`?view=all`）。排序：可触达在前，按 GMB 分从高到低。

| key | 中文 | 权重 | 需谷歌地图数据 |
|---|---|---|---|
| unclaimed | 地图商家资料疑似未认领（无 owner） | 25 | 是 |
| few_reviews | 评论数少（<50） | 20 | 是 |
| low_rating | 评分偏低（<4.2） | 15 | 是 |
| nap_mismatch | 地图与官网 名称/地址/电话 不一致 | 15 | 是 |
| no_hours | 地图缺营业时间 | 10 | 是 |
| few_photos | 照片少（<10，仅在 gosom 返回了图片数时判断） | 10 | 是 |
| generic_category | 仅一个笼统类目（如只有 Restaurant） | 10 | 是 |
| no_description | 缺商家描述 | 10 | 是 |
| no_site_on_maps | 地图资料未关联网站 | 10 | 是 |
| no_https | 官网无 HTTPS | 10 | 否 |
| slow_site | 官网加载慢（>3s） | 8 | 否 |
| no_title_meta | 缺 title / meta 描述 | 8 | 否 |
| no_schema | 缺 LocalBusiness 结构化数据 | 8 | 否 |
| not_mobile | 非移动端适配（无 viewport） | 8 | 否 |
| site_no_maps | 官网未链接/嵌入谷歌地图 | 5 | 否 |

只用 OSM 的商家拿不到评论、营业时间、类目、描述等地图资料，只会检查官网项，并标记「需谷歌地图数据」；勾选 Google Maps 重跑即可补全（旧线索会自动重新检查）。CSV 里有 `points_zh`、`points_en`（可直接粘进开发信）、`point_keys`、`gmb_score`、`best_email`、`reachable` 等列。阈值在 `internal/score/score.go` 里可改。

本工具**不发送任何邮件**。

## 增量扫描与半径说明

| 场景 | 行为 |
|---|---|
| 半径留空或 `0` | **全城**：用 Nominatim 返回的边界框铺格子，整片扫描（不是市中心一小圈） |
| 半径 `5000` | 以地名解析出的中心点为圆心，5 km 内铺格子扫描 |
| 早上扫过 5 km，下午改 10 km（勾选增量） | 只扫 **5–10 km 圆环**里尚未覆盖的格子；5 km 内已有线索直接复用 |
| 同一半径再跑一次（增量开） | 已扫格子全部跳过，几乎不发新请求 |
| 街道/行政区（可选） | 填了会拼进地理编码（如 `静安, 上海`）；**不强制**。不填则按城市中心/边界框 |
| 取消「增量扫描」 | 忽略已有覆盖，按当前半径/全城重新扫（仍会合并线索库去重） |

覆盖状态按 `(规范化城市, 行业, 数据源)` 存格子；线索库按 `(城市, 行业)` 跨任务累积。Web 上点「查看已有覆盖」可看已扫格数和大约已盖半径。

格子默认边长 2500 m，单次每个数据源最多扫 40 格（`-cell-size` / `-max-cells` 可调）。Google Maps 每个格子会起一次 gosom 任务，耗时更长，建议先用 OSM 验证流程。

## 方式一：原生运行（不用 Docker，推荐）

需要 Go 1.26+，或者直接在 Releases 页面下载编译好的二进制（推 `v*` 标签后由 GitHub Actions 自动打包 Windows/macOS/Linux 版本）。

```bash
git clone https://github.com/timmy2048/leadfinder && cd leadfinder
go build -o leadfinder ./cmd/leadfinder        # Windows: go build -o leadfinder.exe ./cmd/leadfinder
```

### 只用 OpenStreetMap（最快、零依赖）
```bash
# 5 km
./leadfinder run -category cafe -location "上海" -country cn -radius 5000 -max-results 20 -out leads.csv -out-unreachable no-email.csv
# 扩大到 10 km：只扫圆环
./leadfinder run -category cafe -location "上海" -country cn -radius 10000 -incremental=true -out leads.csv
# 全城（边界框铺格）
./leadfinder run -category cafe -location "Bath, UK" -country gb -radius 0 -max-cells 20 -out leads.csv
```

### 加上 Google Maps（自动下载 gosom 并安装浏览器）
```bash
./leadfinder run -category dentist -location "Austin, TX" -country us -radius 5000 -limit 20 -gmaps-on -gmaps-download -out leads.csv
```
- `-gmaps-download`：第一次运行时从 gosom 的 GitHub Release 下载对应系统的二进制到 `./bin/`，并作为子进程以 Web/API 模式启动（`127.0.0.1:8081`）。gosom **第一次启动会自动安装 Playwright 浏览器**，需要几分钟。
- 也可以自己下载 gosom，用 `-gmaps-bin /path/to/google-maps-scraper` 指定；或者用 `-gmaps http://host:8080` 连一个已经在运行的 gosom。
- gosom 只发布 amd64 版本：**Apple Silicon Mac** 通过 Rosetta 2 运行（`softwareupdate --install-rosetta`）；Linux arm64 请改用 Docker 或自己编译 gosom。
- Linux 原生运行 Playwright 可能缺系统库，可以运行一次 `npx playwright install-deps chromium`，或者改用 Docker。
- 一个 Google Maps 格子通常要数分钟，全城 + 多格会很久，请配合 `-max-cells`。

### Web 界面
```bash
./leadfinder serve -gmaps-download          # 只用 OSM 的话不加任何 gmaps 参数
# 打开 http://localhost:8090
```
Windows 在 PowerShell 里运行 `.\leadfinder.exe serve -gmaps-download`。

## 方式二：Docker（可选）
```bash
docker compose up -d --build
# LeadFinder: http://localhost:8090   gosom 自带界面: http://localhost:8080
```
`docker-compose.yml` 包含两个服务：`leadfinder` 和官方原版镜像 `gosom/google-maps-scraper`（没有改动）。数据保存在 `./data`。

## 参数
| 参数 | 说明 |
|---|---|
| `-category` | 行业，例如 `restaurant`、`dentist`、`hotel`、`gym`；也可以直接写 OSM 标签，如 `amenity=cafe` |
| `-location` / `-country` | 城市/地区和 ISO 国家代码（用于 Nominatim 地理编码） |
| `-area` | 可选更细的行政区/街道，拼进地理编码；不填则不做街道级强制 |
| `-lang` | Google Maps 语言，默认 `en` |
| `-radius` | 搜索半径（米）。**`0` 或省略 = 全城边界框** |
| `-incremental` | 增量扫描，默认 `true`：跳过已扫格子，扩大半径只扫圆环 |
| `-cell-size` / `-max-cells` | 覆盖格子边长（默认 2500 m）和单次每源最多格子数（默认 40） |
| `-limit` | 每个格子向数据源请求的数量提示（不是总数） |
| `-max-results` | **全局总上限**：最多输出多少家「有邮箱」的商家（0=不限）。凑够有邮箱后停止爬官网；格子仍扫完本次计划（或 `-max-cells`），不再按候选数 4 倍提前停 |
| `-only-reachable` | 主 CSV 只放有邮箱的商家（默认 true）；设 false 则把暂不可触达的追加在后面 |
| `-out-unreachable` | 另存「暂不可触达」CSV 的路径 |
| `-osm` / `-gmaps-on` | 数据源开关（OSM 默认开） |
| `-social` | 从社媒公开主页补抓邮箱（默认关） |
| `-smtp` | SMTP 邮箱存在性检查，**需要 25 端口出站**（大多数家庭宽带和云服务器默认封禁，被封时结果会显示 unknown） |

邮箱状态：`valid`（语法和 MX 正常；开 SMTP 时表示服务器确认可投递）、`risky`（一次性邮箱或 catch-all）、`invalid`、`unknown`（DNS/SMTP 查询失败）。

## 架构
```
cmd/leadfinder        命令行入口（serve / run）
internal/coverage     覆盖格子、圆环/差分、规范化 key
internal/osm          Nominatim 地理编码（含 bbox）+ Overpass 查询
internal/gmaps        gosom REST 客户端、CSV 解析、原生二进制下载和启动
internal/dedupe       去重合并
internal/crawl        colly 爬官网、邮箱提取、SEO 信号
internal/social       可插拔的社媒补全（Enricher 接口）
internal/verify       邮箱验证
internal/score        可触达判定 + GMB 机会分（提升点目录）
internal/store        SQLite（任务、单次结果、coverage_cells、leads 库）+ CSV 导出
internal/pipeline     编排（按格扫描、增量、合并线索）
internal/web          Web 界面 + JSON API（/api/jobs、/api/coverage）
```

## 合规说明（请务必阅读）
- **Google Maps / Instagram / Facebook / TikTok 的服务条款都禁止自动化抓取**。使用风险自负：控制频率和数量，只收集商家对外公开的信息，不要抓个人数据。OSM 数据采用 ODbL 许可，使用时需注明 © OpenStreetMap contributors。公共 Nominatim/Overpass 服务请遵守它们的使用政策（Nominatim 每秒最多 1 次请求，不要大批量查询）。
- 爬官网时遵守 robots.txt（colly 默认开启）。
- 联系商家时遵守 **GDPR**（欧盟/英国：B2B 也需要有合法利益依据，要告知数据来源，并在对方要求时删除）、**CAN-SPAM**（美国：提供真实身份、地址，以及**一键退订**）、中国《个人信息保护法》等当地法律。**每封邮件都必须提供退订方式，并尊重退订请求。**
- 本项目仅供个人学习和合规调研使用。

## CI / 自动发布
推送到 `main` 会跑测试；推 `v*` 标签时 Actions 会编译 Windows/macOS/Linux 二进制。
