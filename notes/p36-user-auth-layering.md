# P36 · 用户、鉴权与分层测试（webook 主线）

> 这章学完你能干什么：说清一个用户系统的分层骨架（handler → Store 接口 → 双实现）、密码与 token 的教学实现边界（sha256+盐、手写 HS256）、面向接口给测试带来的收益（双 Store 同套用例），并知道生产化还差哪三块（验证码 lua、OAuth2 state、双 token 与配置）。素材来自 webook 课程线（gin + GORM + MySQL），本章项目锚点是标准库版的 minifeed：同一个分层语义，不同技术栈。前置：P30（接口）、P34（1.22 路由）、P35（治理边界）。

## 1. 分层：从 handler 到 Store 接口

webook 的起点是反面教材：Handler 直接拿 gorm.DB 写查询，路由、校验、业务、SQL 挤在一个函数里。解法是三层：service 领域服务、repository 整体存储抽象、dao 数据库操作，加上 domain 领域对象。〔W02-2 p14-15〕三层的职责辨析是这章的第一块硬知识：repository 是整体存储抽象，底层可以是 ES/MySQL/MongoDB；dao 只代表数据库；service 组合 repository/domain 完成业务；domain 是业务对象在程序中的直接反应。〔W02-2 p16-17〕

dao 与 domain 分开定义的动机在字段类型上看得最清楚：dao.User 直接映射表列，Ctime/Utime 用 int64 存 UnixMilli；domain.User 是业务概念，CreatedAt 用 time.Time，JSON 字段还会转结构体。转换归存储实现负责，不污染业务层。〔W02-2 p20〕

GORM 的 CRUD 基线（Open → AutoMigrate → Create/First/Update/Delete）是素材课的技术栈。〔W02-2 p6〕minifeed 用 database/sql + modernc.org/sqlite（纯 Go 无 cgo，测试零 Docker 依赖）实现同一语义；差异章内明说：没有 AutoMigrate，建表语句写在 schema 常量里。

minifeed 的分层落点：user/store.go 定义 User 领域模型（CreatedAt time.Time）、Store 接口（Create/GetByEmail/GetByID 三方法）、MemoryStore 与 SQLiteStore 双实现。SQLiteStore 的 scanUser 收敛列映射，created_at 存 UnixMilli 读时转 time.Time，就是 K10 的 dao/domain 边界在单文件里的形状。〔W02-2 p20〕

面向接口加依赖注入的效果是：调用方依赖所需行为，初始化时传入实现，并不要求所有组件间调用都经过接口。〔W04-3 p24〕webook 用 UserHandler.RegisterRoutes 把用户路由收在 handler 上（/users/signup、/users/login、/users/profile）。〔W02-1 p22〕minifeed 的 newMux 是集中注册形态，server struct 持 user.Store 接口字段：换实现只动 openStore 一行。

〔实测〕双实现同套测试：`cd minifeed && go test -race -count=1 ./user/`，user_test.go 的 exercise(t, Store) 被 Memory 和 SQLite 两个子测试各跑一遍，全部 ok。这是 K7/K8 分层的直接收益：测试不关心底下是 map 还是 sqlite 文件。

## 2. HTTP 层：路由、绑定与统一出口

gin 的 Engine 是逻辑上的 Web 服务器，一个进程可以建多个 Engine 监听不同端口。gin.Default() 自带 Logger+Recovery，gin.New() 是裸引擎。〔W02-1 p6〕路由注册是每个 HTTP 方法一个快捷方法，参数 (路由规则, 处理函数)。〔W02-1 p12〕

minifeed 用标准库 1.22 的方法+路径模式："POST /users"、"GET /users/{id}"，PathValue 取参数。不引 gin。IM 素材同样把 go1.22+ 路由语法列为 P41 前置。路由语义两栈同构，写法差异一行讲完。

Bind 的语义是关键教学点：gin 按 Content-Type 决定反序列化方式（application/json 走 JSON），Bind 出错直接写错误响应，handler 里 `if err := ctx.Bind(&req); err != nil { return }` 后面不用再处理。〔W02-1 p31〕minifeed 的 decode[T] 泛型保留同一语义但把决定权还给 handler：失败写 400 返回 ok=false，调用方 `in, ok := decode[signupReq](w, r); if !ok { return }`。为什么改：泛型版本不吞错误细节，测试能区分 400 与 500。

中间件的本质是"进入 handler 前后的统一入口"：Gin 的 Use(middleware ...HandlerFunc) 全局挂载，HandlerFunc 就是 func(*Context) 的衍生类型，即 AOP 思路，适合所有业务都关心的横切问题。〔W02-1 p44-45〕webook 的 cors middleware 三配置：AllowCredentials（能否带 cookie 等认证信息）、AllowHeaders（业务请求可带的头）、AllowOriginFunc（origin 白名单函数）。〔W02-1 p43-47〕minifeed 骨架不实装 cors（同机 httptest 无跨域），章内只记落点：若前后端分域名，在 newMux 外再包一层。

注册方式的两难：集中注册（main 里全注册，打开见全部但找起来费劲）vs 分散注册（handler 自注册，有条理但找路由不好找）。〔W02-1 p23-24〕minifeed 的 newMux 三行 HandleFunc 是集中式最小形态，路由表一眼可见；路由超过一屏时再考虑 handler 自注册。

〔实测〕`curl -s -w " | HTTP %{http_code}\n" -X POST localhost:8080/users -d '{"email":"a@b.c","nick":"alice","password":"hunter2secret"}'` 得 `{"id":1} | HTTP 201`；同一请求重发得 `dup HTTP 409`；弱密码 `{"email":"a@b.c","nick":"x","password":"123"}` 得 `weak HTTP 400`。邮箱占用是正常业务分支返回 409 而不是 500；参数不合法 400；成功 201 带 id（对照 webook D2 教学简化的生产化语义）。writeJSON 统一出口先 Content-Type 后 WriteHeader（P34 已讲的顺序铁律，写反了 header 静默丢失）。


## 3. 密码与 token：auth 包的两个职责

密码存储需要的是密码哈希而不是可逆加密。盐使相同密码通常产生不同的存储值，也让预计算彩虹表更难复用；抵抗暴力猜测则需要可调成本的慢密码哈希，而不仅仅是加盐。〔W02-2 p31〕webook 使用 bcrypt；minifeed 教学版用 `sha256(salt || password)`，每用户生成 16 字节随机盐并存 hex。单次 SHA-256 计算太快，不适合生产密码存储；生产应使用 Argon2id、bcrypt 或 PBKDF2 等专用方案并配置适当成本。教学版仅用于观察盐的生成、拼接和比较，不应原样部署。

token 侧：登录签发用 jwt.NewWithClaims(jwt.SigningMethodHS256, claims) + token.SignedString(key)，token 放响应头返回，前端在 Authorization 带 Bearer。〔W03-1 p25〕JWT 与 Session 的取舍：JWT 免第三方存储、适合分布式、性能高（无 Redis 访问）；缺点是 payload 只是 base64 不是加密，不能放敏感信息，且签出后无法单方面作废。〔W03-1 p30〕

minifeed 的 auth 包手写 HS256 三段式，与 post 工程的手写实现同构。header/payload 各自 json.Marshal 后 RawURLEncoding。签名取 HMACSHA256(part1+"."+part2, key) 作第三段。〔C:go_frame/post/util/jwt.go:29-52〕

Claims 只放 UID 和 Exp 两个字段。ParseToken 的两个安全点：签名比较用 hmac.Equal（恒时比较，防时序侧信道）；Exp 用 Unix 秒判定，过期返回 ErrTokenExpired 与 ErrTokenInvalid 分开。分开是登出与续期讨论的入口。

〔实测〕`go test -race -count=1 -v ./auth/ 2>&1 | grep -E "^--- (PASS|FAIL)"` 四个测试全 PASS：TestHashVerify（同一密码两次 Hash 得到不同密文，盐随机）、TestTokenRoundTrip（签出能解回原 uid）、TestTokenExpiry（过期 token 报 ErrTokenExpired）、TestTokenTamper（payload 改一位即失效，错 key 解不出原 uid）。


登录的错误处理有一条易漏的安全线：401 不区分"没这人"与"密码错"。两类错误同一个响应，防用户枚举（攻击者靠错误消息差异收集注册邮箱列表）。minifeed handleLogin 的实现：GetByEmail 的未知错误进 500 通道，ErrNotFound 与 Verify 失败汇入同一个 401 "bad credentials"。

〔实测〕`MINIFEED_TOKEN_KEY=smoke-key... MINIFEED_DB=/tmp/mf.db` 起进程，login 得 `restart-relogin HTTP 200`；kill 后同环境变量重启，旧 token 仍过 ParseToken、重登仍 200（sqlite 落盘 + 密钥固定）。密钥不配则随机生成，进程重启旧 token 全失效，密钥管理是部署问题。Session 方案没有这个问题，因为状态在服务端；这是 K15 取舍的运行时体现。


Session 侧的素材只记结论：固定过期时间的痛点（设 10 分钟，9:59 还能用、10:01 被踢）催生四种刷新策略（每次访问都刷/快过期才刷/固定间隔刷/长短 token），刷新逻辑放登录校验 middleware 顺手做。〔W03-1 p16〕〔W03-1 p17-18〕长短 token 是 W05-2 的主线，本章延伸节展开；minifeed 的 auth 中间件只做校验，刷新是 P37 后的练习位。

## 4. 还差什么：验证码、OAuth2 与配置（生产视角）

minifeed 骨架没有这三块，素材把它们补齐到生产形态。章内按"加在 minifeed 哪一层"叙述，代码是练习位。

验证码与登录是弱耦合：危险操作二次验证、改密码都用验证码，所以它是独立模块；短信还可能换供应商。〔W04-2 p10-11〕两安全线：控制频率（同号一分钟一次、有效期十分钟、系统限流兜底）+ 防暴力破解（验证通过即作废；三次失败作废；失败只说"不对"不提示次数）。〔W04-2 p26〕

“检查后执行”若没有原子性就有竞态：两个实例都看到“没发过”并各自发短信。进程内 mutex 不能协调不同进程；可以用 Redis Lua 将检查与状态更新作为原子操作，或者采用支持所需原子条件更新的其他共享存储。注意短信发送本身在 Redis 之外，还需设计发送失败与重试的处理。〔W04-2 p30-31〕

发码 lua 三分支。ttl==-1（key 无过期时间，疑似误操作）拒绝。ttl==-2 或 ttl<540（不存在或已过一分钟）时 set code、expire 600、cntKey=3。否则一分钟内已发，拒绝。〔W04-2 p32〕

minifeed 的 MemoryStore 把查重放在同一进程的锁内，因此该操作在这份实现中原子；它没有实现验证码。多副本需要共享协调机制，不能依赖各进程自己的锁。P35 §5 讨论了相同的作用域边界。

用户表的并发创建走 FindOrCreate 三步：先查（大多数人命中直接返回）→ 查不到 Create → Create 报唯一索引冲突说明并发，再查一次返回。〔W04-2 p45-46〕minifeed 的 MemoryStore.Create 线扫查重 + 锁内自增 ID 是第一步的进程内版；SQLiteStore 靠 email UNIQUE 约束，冲突即 ErrEmailTaken（409），就是第三步的兜底。

扫码登录本质是 OAuth2：用户授权第三方应用拿 access_token，第三方视为登录。〔W05-1 p8〕

state 防 CSRF 的攻击链值得完整复述一遍：攻击者先弄一个绑定自己微信的临时授权码 A，伪造页面诱导已登录用户点击，带着用户 Cookie + 攻击者的授权码 A 完成绑定，攻击者之后可扫码登录看用户数据。核心是"攻击者让你用他的临时授权码绑定账号"。〔W05-1 p28〕防御是登录/绑定前发随机 state 存服务端（或 JWT 短 token），回调时校验，三错即敌情（缺、错、重放）。

minifeed 落点：oauth/ 包 + 回调 handler，state 校验在换 token 之前。

配置的来源优先级：命令行（我主动输入的最权威）> 环境变量 > 配置文件 > 远程配置中心。〔W05-3 p5-6〕

读取粒度反模式：不推荐 viper.GetString("redis.addr") 单字段散读，推荐 IoC 内定义内部结构体 + UnmarshalKey("db", &c) 整体注入。〔W05-3 p11-12〕minifeed 现在用三个环境变量（MINIFEED_ADDR/MINIFEED_TOKEN_KEY/MINIFEED_DB），正是第一、二级。配置涨到五个以上再引 viper，加载限定在 main（配置操作限定在初始化，IoC 的纪律）。

## 5. 组装：wire、DI 与 Singleton 实测

webook 的初始化七步链（config → db → redis → dao → repository/cache → service → handler）手写就是"非依赖注入写法"：NewUserRepositoryV1 自己 gorm.Open+redis.NewClient，缺点是深度耦合初始化过程、依赖变更跟着改、测试不友好。〔W04-3 p5-6〕

wire 的两步使用：wire.go 里 wire.Build(注册全部构造函数) + 随便 return 一个值，跑 wire 命令生成 wire_gen.go 真正的初始化方法。文件带 go:build wireinject 编译标签。〔W04-3 p7-8〕

原理是代码生成期分析构造函数依赖。限制是不支持单例，多次调用 InitDB 会生成多个 gorm.DB。〔W04-3 p9〕构造函数返回接口时用 wire.Bind(new(redis.Cmdable), new(*redis.Client)) 建立实现到接口的绑定。〔W04-3 p15〕

minifeed 的组装是手动装配的最小形态：main() 里 openStore 按环境变量选 Memory 或 SQLite，&server{store: store, tokenKey: key, log: log} 一行注入。依赖图只有两条边（server→Store、server→tokenKey），引 wire 属于杀鸡用牛刀；wire 的价值在依赖图十几条边时自动拓扑排序。

容器路线的实测证据来自 ioc 素材：golobby 容器的 Singleton 与 Transient 行为对照，sort_poster NamedSingleton 两次 NamedResolve 只打一次"创建RecByPoster实例"，sort_time NamedTransient 每次 Resolve 都打一次。rec_test.go 就是此对照。〔C:go_frame/ioc/service/rec_test.go:14-57〕

Spring 读者对照：singleton/prototype 语义一一对应，区别在 Spring 靠注解扫描、Go 显式工厂函数。

三条装配路线的取舍：

| 路线 | 代表 | 依赖图小时 | 依赖图大时 | 单例 |
|---|---|---|---|---|
| 手动装配 | minifeed main() | 最省，一眼见全貌 | main 膨胀 | 自己写 sync.Once |
| wire 代码生成 | webook wire.go | 生成代码比手写多 | 编译期排序，缺依赖编译报错 | 不支持，注意多次调用 |
| 容器运行时 | golobby/p5 di 容器 | 注册样板成本高 | 支持单例/Transient 作用域 | 容器内置 |

p5-deepsearch §依赖注入 已讲过手写容器的单例语义（第一次 Invoke 才构造，第二次拿同一实例），minifeed 不引容器，直接赋值。

## 6. 测试金字塔：单测、mock、集成与 TDD 分界

单测 vs 集成测试的定位：单测针对每个方法单独验证正确性、不依赖任何第三方组件，讲究快速运行-发现-修复-重测循环；集成测试验证组件配合，先单测后集成。〔W04-4 p5-6〕Table Driven 三段：测试用例定义（匿名结构体）+ 具体用例列表 + 遍历执行（t.Run 内 mock → 执行 → 断言）。〔W04-4 p12-15〕minifeed 的 user_test.go 就是三段形状：name/name/'want' 字段 + 用例列表 + exercise 循环。

Handler 初始化难题用 mock 解决：不一路构造到 DAO 准备数据，而是 mock UserService。〔W04-4 p24〕

gomock 铁律：ctrl := gomock.NewController(t) + defer ctrl.Finish()。先 EXPECT() 再传条件再 .Return()。设计几个模拟调用就要全部用上且顺序对上。〔W04-4 p28〕

DAO 测试用 sqlmock：GORM 没暴露接口没法生成 mock。sqlmock 无需代码生成，ExpectBegin/ExpectExec/ExpectCommit 编排，最后 ExpectationsWereMet() 验证。〔W04-4 p66-67〕

minifeed 不引 gomock，用三种更轻的替代，各自的适用边界：

| 层 | 素材课做法 | minifeed 做法 | 取舍 |
|---|---|---|---|
| handler | httptest + mock Service | httptest 走 newMux + 真 MemoryStore | 接口小，fake 比 mock 链省 |
| service/存储 | gomock 生成 | 双 Store 同套 exercise | Store 三方法手写 fake 十行 |
| dao/SQL | sqlmock | 真 sqlite 文件（现代纯 Go 驱动） | sqlmock 为"无接口+贵依赖"而生，这里两个条件都不占 |

gomock 的位置没被推翻：接口大（十几方法）、EXPECT 条件复杂时，手写 fake 会膨胀成第二个实现，mockgen 才划算。素材的 Handler 测试五步与 mockgen 三参数是那个场景的工具。

集成测试两条铁律：直接使用的第三方依赖（Redis/MySQL）要验证状态；调用别人的接口只验证返回响应；永远不要让测试用例互相依赖（重构即崩溃）。〔W04-5 p12〕minifeed 的 main_test.go 用 httptest 起 newMux 全链路断言（signup 201 → dup 409 → login 拿 token → profile 200/403），sqlite 用 /tmp 临时文件，用例之间无共享状态。

TDD 的分界：坚持用，除非连写测试时间都没有；工具库/基础中间件以单元测试为主；业务开发以集成测试为主。〔W21-1 p53〕minifeed 的 auth 包是工具库（纯函数，单测覆盖）；HTTP 层是业务边界（集成测试覆盖）；分层抽象本身（Store 接口）的测试收益在 user_test.go 的双实现同套用例里直接可见。

测试金字塔的下一层（P37 钩子）：性能瓶颈两处在加密算法（注册/登录都耗 CPU）和数据库查询，优化思路是 Redis 先查、未命中回落数据库并回写缓存。〔W04-1 p12〕UserCache 放 repository 层，是 P37 counter/post 两包的前置。

## 7. 小结与边界

minifeed 的 400 行骨架回答了"用户系统的最小正确形状"：Store 接口隔离存储、handler 只管 HTTP、auth 包收密码与 token 两个纯函数职责。三条生产化差距列清：密码哈希换 bcrypt/argon2（W02-2#K15 第三级）；token 换 golang-jwt/jwt/v5 并接入刷新（W03-1#K8 第四种策略）；多副本部署时 Session 状态或 token 黑名单挪 Redis（W05-2 登出困难的解法）。

双 token 与登出是延伸节的主角：access_token 短、高频访问资源易泄露。refresh_token 长且仅登录与刷新时使用，过期后换新 access。〔W05-2 p3-4〕

JWT 登出的困难在于已签发的 token 不会自行获知撤销。若需要在过期前立即撤销，多实例必须共享撤销状态，例如 Redis 黑名单或数据库；若允许等短期 token 过期，也可以不逐次查询黑名单，但撤销不是即时的。〔W05-2 p13-14〕

ssid 方案：登录生成 UUID ssid 放进长短两个 token，校验与刷新都查 ssid 是否已进 Redis 黑名单，退出时标记不可用。〔W05-2 p16-17〕minifeed 不实装（无 Redis 依赖），延伸练习给出落点：auth 包加 ssid 字段 + 黑名单接口。

SSO 的三进程登录链（sso 中心发 sso_token，app 校验后种自己的 session）见 post 工程的可运行实现：〔C:go_frame/post/login/sso/app1/main.go:43-128〕。p5-deepsearch 的鉴权小节（§改一处：打开鉴权）讲的是 Bearer 语义，不含 JWT/SSO 实现，延伸阅读以 post 代码为准。

## Take-away

1. repository 是整体存储抽象，dao 只代表数据库；domain 时间用 time.Time，UnixMilli 转换归存储实现（W02-2#K8/K10）。
2. 面向接口 + 依赖注入 = 编译时依赖接口、初始化时注入实现；双 Store 同套测试是直接收益（W04-3#K10）。
3. Bind 失败已写响应是 gin 语义；泛型 decode 把 ok 返还给 handler，测试能区分 400/500（W02-1#K12）。
4. 401 不区分"没这人"与"密码错"：防用户枚举是登录接口的安全线。
5. SHA-256 加盐仍太快，只作教学；生产密码存储使用专用密码哈希并设置成本（W02-2#K15）。
6. JWT payload 是 base64url 编码不是加密；签名比较要恒时（hmac.Equal）；过期与无效分开报（W03-1#K15）。
7. 多副本的“检查-做某事”需要共享原子机制；单进程可在同一把锁内完成查重（W04-2#K16）。
8. 装配三路线按依赖图规模选：手动 → wire（代码生成）→ 容器（运行时单例）。
9. TDD 分界：工具库单测为主，业务集成为主（W21-1#K45）。

## 章末参考

- 命令速查：`MINIFEED_ADDR=:8080 MINIFEED_DB=/tmp/mf.db MINIFEED_TOKEN_KEY=xxx` 三环境变量；`go test -race -count=3 ./...`（minifeed 三包）
- 源码（本机 go1.26.4）：server.go 的 newMux L72、handleSignup L107、handleLogin L135、handleProfile L158
- 源码续：auth/auth.go 的 Hash L17、SignToken L46、ParseToken L62；user/store.go 的 Store L46、MemoryStore L58、SQLiteStore L86
- 原材料单元：W02-1（gin 路由与中间件）、W02-2（GORM 分层与加密）、W03-1（Session/JWT）、W04-2（验证码 lua）、W04-3（wire 与面向接口）、W04-4（mock 测试）、W04-5（集成测试）、W05-1（OAuth2 state）、W05-2（双 token 与 ssid）、W05-3（viper 配置）、W21-1（TDD 分界）
- 代码素材：C:go_frame/post/util/jwt.go:29-52（手写 JWT 同构参照）；C:go_frame/post/login/sso/app1/main.go:43-128（SSO 登录链）；C:go_frame/ioc/service/rec_test.go:14-57（Singleton/Transient 实测）
- 延伸：golang-jwt/jwt/v5（生产 token）；golang.org/x/crypto/bcrypt（生产密码）；spf13/viper（配置）；github.com/google/wire（装配）
- 旧章交叉：p30 §receiver 是第一个参数 与 §接口的运行时（方法值与接口值基础）、p34 §4 业务复用（1.22 路由与方法值 handler、统一出口顺序铁律）、p2_5 §表驱动测试（三段形状）、p21 §-race：动态分析武器（并发测试武器）、p35 §5 阈值从哪来（集群化边界，多副本 lua 的同一逻辑）、p5-deepsearch §依赖注入/§优雅关停（容器与 SIGTERM）、p13 §13.1 RED（观测位）
- 明确不引：p5-deepsearch 无 §JWT/§SSO 小节（dispo 指引名不实，SSO 引 post 代码）；p6（实装复盘，无鉴权内容）

## 练习：p36minifeed

先跑全绿：`cd minifeed && go test -race ./...`

1. 预测：同一密码两次 signup，password_hash 与 salt 是否相同？答案在 auth.Hash 的盐生成里（随机，所以不同）。
2. 预测：login 用不存在的邮箱与用错误密码，响应码与响应体是否可区分？答案：不可，都是 401 + "bad credentials"（防枚举）。
3. 观察：删掉 /tmp/mf.db 再重启，旧 token 是否仍有效？（tokenKey 不变则有效；换成 session 方案即失效，体会 K15。）
4. 改造：给 Store 加 UpdateNick(id, nick)，双实现各一份，测试补一条。
5. 改造：auth 包加 ssid 字段（W05-2#K9），登录发 ssid，ParseToken 后查黑名单（内存 map 版即可）。
6. 测试：给 handleProfile 补"登录用户查别人 id"的 403 用例；用 fake Store 替换 SQLiteStore 跑 main_test，验证接口抽象的测试收益。
