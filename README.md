# LeadFinder

个人用的 B2B 商家线索工具（适合做 SEO 服务找客户）。输入**行业 + 地点**，从 **Google Maps**（通过 [gosom/google-maps-scraper](https://github.com/gosom/google-maps-scraper)）和 **OpenStreetMap**（Overpass API）拉取商家信息，然后：

- 按域名、电话、坐标+名称去重合并
- 用 [colly](https://github.com/gocolly/colly) 爬商家官网首页和联系页提取邮箱。支持 `mailto:`、正则匹配、`[at]`/`(dot)` 这类混淆写法、Cloudflare `data-cfemail` 解码，优先排同域名邮箱
- 可选：从商家的 Instagram/Facebook/TikTok 主页补抓邮箱。只做匿名公开访问，不登录，**默认关闭**
- 用 [AfterShip/email-verifier](https://github.com/AfterShip/email-verifier) 验证邮箱：语法、MX 记录、一次性邮箱；SMTP 检查可选
- **SEO 需求评分**（0–100，越高越需要 SEO）：无网站、网站打不开、无 HTTPS、网站慢、缺 title / meta 描述、评论少、评分低
- 结果存 SQLite（纯 Go，无需 CGO）。提供 Web 界面（创建任务、看进度、看结果表、导出 CSV）和命令行

> ⚠️ 本工具**没有发邮件功能**，也不会加。

## 方式一：原生运行（不用 Docker，推荐）

需要 Go 1.26+，或者直接在 Releases 页面下载编译好的二进制（推 `v*` 标签后由 GitHub Actions 自动打包 Windows/macOS/Linux 版本）。

```bash
git clone https://github.com/timmy2048/leadfinder && cd leadfinder
go build -o leadfinder ./cmd/leadfinder        # Windows: go build -o leadfinder.exe ./cmd/leadfinder
```

### 只用 OpenStreetMap（最快、零依赖）
```bash
./leadfinder run -category restaurant -location "Bath, UK" -country gb -radius 3000 -limit 20 -out leads.csv
```

### 加上 Google Maps（自动下载 gosom 并安装浏览器）
```bash
./leadfinder run -category dentist -location "Austin, TX" -country us -limit 20 -gmaps-on -gmaps-download -out leads.csv
```
- `-gmaps-download`：第一次运行时从 gosom 的 GitHub Release 下载对应系统的二进制到 `./bin/`，并作为子进程以 Web/API 模式启动（`127.0.0.1:8081`）。gosom **第一次启动会自动安装 Playwright 浏览器**，需要几分钟。
- 也可以自己下载 gosom，用 `-gmaps-bin /path/to/google-maps-scraper` 指定；或者用 `-gmaps http://host:8080` 连一个已经在运行的 gosom。
- gosom 只发布 amd64 版本：**Apple Silicon Mac** 通过 Rosetta 2 运行（`softwareupdate --install-rosetta`）；Linux arm64 请改用 Docker 或自己编译 gosom。
- Linux 原生运行 Playwright 可能缺系统库，可以运行一次 `npx playwright install-deps chromium`，或者改用 Docker。
- 一个 Google Maps 任务通常要 3 分钟以上（gosom 的最短运行时间），可以用 `-gmaps-max-sec` 设置上限。

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
| `-location` / `-country` | 地点和 ISO 国家代码（用于 Nominatim 地理编码） |
| `-lang` | Google Maps 语言，默认 `en` |
| `-radius` / `-limit` | 搜索半径（米）和每个数据源的数量上限 |
| `-osm` / `-gmaps-on` | 数据源开关（OSM 默认开） |
| `-social` | 从社媒公开主页补抓邮箱（默认关） |
| `-smtp` | SMTP 邮箱存在性检查，**需要 25 端口出站**（大多数家庭宽带和云服务器默认封禁，被封时结果会显示 unknown） |

邮箱状态：`valid`（语法和 MX 正常；开 SMTP 时表示服务器确认可投递）、`risky`（一次性邮箱或 catch-all）、`invalid`、`unknown`（DNS/SMTP 查询失败）。

## 架构
```
cmd/leadfinder        命令行入口（serve / run）
internal/osm          Nominatim 地理编码 + Overpass 查询
internal/gmaps        gosom REST 客户端、CSV 解析、原生二进制下载和启动
internal/dedupe       去重合并
internal/crawl        colly 爬官网、邮箱提取、SEO 信号
internal/social       可插拔的社媒补全（Enricher 接口）
internal/verify       邮箱验证
internal/score        SEO 需求评分
internal/store        SQLite 存储 + CSV 导出
internal/web          Web 界面 + JSON API（/api/jobs）
```

## 合规说明（请务必阅读）
- **Google Maps / Instagram / Facebook / TikTok 的服务条款都禁止自动化抓取**。使用风险自负：控制频率和数量，只收集商家对外公开的信息，不要抓个人数据。OSM 数据采用 ODbL 许可，使用时需注明 © OpenStreetMap contributors。公共 Nominatim/Overpass 服务请遵守它们的使用政策（Nominatim 每秒最多 1 次请求，不要大批量查询）。
- 爬官网时遵守 robots.txt（colly 默认开启）。
- 联系商家时遵守 **GDPR**（欧盟/英国：B2B 也需要有合法利益依据，要告知数据来源，并在对方要求时删除）、**CAN-SPAM**（美国：提供真实身份、地址，以及**一键退订**）、中国《个人信息保护法》等当地法律。**每封邮件都必须提供退订方式，并尊重退订请求。**
- 本项目仅供个人学习和合规调研使用。

## CI / 自动发布（需手动启用）
`docs/workflows/` 里有 `ci.yml`（测试）和 `release.yml`（推 `v*` 标签后自动编译 Windows/macOS/Linux 二进制）。把它们复制到 `.github/workflows/` 并提交即可启用（推送 workflow 文件需要带 `workflow` 权限的 token）。
