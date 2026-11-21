package fn

// 飞牛影视私有接口的固定 API 密钥（逆向自 fntv-electron / trimemedia-web）。
// 真机验证（2026-11，fnOS 0.9.8 front / mediasrv）有效。
//
// TODO(vendor): 这些值不应长期硬编码在开源仓库中；发布时改为从配置注入，
// 并在启动自检里验证签名是否仍然有效，失效时给出明确告警。
const (
	APIKey    = "NDzZTVxnRKP8Z0jXg1VAMonaG8akvh"
	APISecret = "16CCEB3D-AB42-077D-36A1-F355324E4237"
	AppName   = "trimemedia-web"

	// PlayerUA 飞牛官方播放器 UA。网盘直链（尤其夸克/光驱）用它请求才不限速。
	PlayerUA = "1.12.0 (com.trim.media; build:1120009; Android 16) Flutter/3.11.5"

	// DeviceFingerprint 32 位十六进制设备指纹，stream 接口的 ip 字段。
	DeviceFingerprint = "16a9d43e38a5f992b3f1c44d68940523"
)

// PathPrefix 所有 v1 接口的公共前缀。
const PathPrefix = "/v/api/v1/"

// LoginPath 登录接口（v2，注意不是 v1）。
const LoginPath = "/v/api/v2/user/loginByPassword"

// CloudStorageTypeSTRM 表示片源是 STRM 文件（飞牛把其中的 URL 解析后放进 direct_link_qualities）。
const CloudStorageTypeSTRM = 9001
