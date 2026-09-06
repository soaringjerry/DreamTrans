import type { Locale } from '../i18n'

export const LEGAL_EFFECTIVE_DATE = '2026-09-06'
export const LEGAL_OPERATOR = 'Coyume Pty Ltd'
export const LEGAL_PRODUCT = 'Yufolo'
export const LEGAL_CONTACT_EMAIL = 'support@coyume.com'

export type LegalKind = 'privacy' | 'terms'

export type LegalBlock =
  | { type: 'p'; text: string }
  | { type: 'ul'; items: readonly string[] }

export interface LegalSection {
  id: string
  heading: string
  blocks: readonly LegalBlock[]
}

export interface LegalDocument {
  kind: LegalKind
  title: string
  summary: string
  sections: readonly LegalSection[]
}

const privacyZh: LegalDocument = {
  kind: 'privacy',
  title: '隐私政策',
  summary:
    '本政策说明 Yufolo（DreamTrans 软件的对外产品名）如何收集、使用、保存和共享你的信息。它按当前软件的实际数据流撰写，适用于由 Coyume Pty Ltd 运营的官方托管服务。若你使用的是他人自行部署的实例，该部署的运营方才是该实例的个人信息处理者，其政策可能与本文不同。',
  sections: [
    {
      id: 'who',
      heading: '1. 我们是谁',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo 由澳大利亚私营公司 Coyume Pty Ltd（下称「我们」）运营。Yufolo 提供实时语音转录、翻译、会话存档、AI 辅助和学习空间。仓库中的软件工程名称为 DreamTrans。',
        },
        {
          type: 'p',
          text: '就本政策提出的请求，请发送至 support@coyume.com；从你的注册邮箱发出可以帮助我们更快核实身份。',
        },
      ],
    },
    {
      id: 'scope',
      heading: '2. 适用范围',
      blocks: [
        {
          type: 'p',
          text: '本政策适用于网站、工作区、管理后台、学习空间，以及官方浏览器扩展（例如 Moodle 同步）在与 Yufolo 服务器通信时处理的信息。',
        },
        {
          type: 'p',
          text: '它不适用于语音识别、机器翻译、大模型或支付等第三方自己的处理活动——那些服务受其自身隐私政策约束。我们会在下文列出主要处理者。',
        },
      ],
    },
    {
      id: 'collect',
      heading: '3. 我们收集哪些信息',
      blocks: [
        {
          type: 'p',
          text: '我们只收集提供服务所需要的信息，主要包括：',
        },
        {
          type: 'ul',
          items: [
            '账户信息：邮箱、显示名、密码哈希、角色、租户归属、邮箱验证状态。我们不保存明文密码。',
            '音频：你授权后，浏览器会采集麦克风和/或系统/标签页音频。实时音频会经我们的服务器转发到语音识别服务商，用于转录和按音频字节计量。云端会话同步不会上传或保存完整录音文件。若你在设置中开启「保存本地录音」，录音分块只写在这台设备的浏览器 IndexedDB 中。',
            '批量转写：若你提交音频文件做批量识别，文件会经我们的服务器转交给语音识别服务商处理。我们自己不保存该文件；它在服务商侧的留存受该服务商的数据保留条款约束，并会被该服务商用于改进其自身模型（见第 6 节）。',
            '转录与翻译文字：带说话人、时间戳的原文、译文及相关元数据。访客模式下主要保存在本机；登录后会同步到我们的数据库，并在本机保留按账号隔离的缓存。',
            '知识与学习内容：你主动上传的文件、可编辑记忆、摘要、笔记、行动项、技能地图、练习记录，以及从学习管理系统同步的派生文本。',
            '计费信息：用量、预留与结算、钱包与赠送额度、套餐、充值档位。银行卡等支付详情由 Stripe 处理，我们不保存完整卡号。',
            '注册赠送资格：重复浏览器注册、网络注册频率和规范化邮箱历史等风险信号可能触发赠送权益暂缓，交由管理员审核。账号仍可使用；可联系 support@coyume.com 请求复核。',
            '技术与安全日志：IP 地址、User-Agent、请求路径、错误与延迟、Cloudflare Ray ID（若经其网络）、注册频率限制所需的有限连接信息。',
            '你主动提供的设置：界面语言、源/目标语言、翻译引擎与提示词、音频输入偏好等。管理员允许时，你填写的第三方 API 密钥只保存在当前标签页的 sessionStorage / 内存中，关闭标签页或退出登录即清除。',
          ],
        },
      ],
    },
    {
      id: 'audio',
      heading: '4. 音频如何处理（请特别注意）',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo 的核心功能需要处理语音。开始录音即表示你理解并同意：',
        },
        {
          type: 'ul',
          items: [
            '实时音频会流经我们的服务器，转发至语音识别服务商（当前官方路径为 Speechmatics 位于欧洲的实时接口），以便生成文字并按转发的音频字节计费。',
            '我们不会把完整录音作为云端会话的一部分保存或同步。产品界面中的「音频保留在你的设备」指的是：Yufolo 云端存的是文字，不是可下载的云端音轨。',
            '本地录音是可选的，由你在设置中开启，仅存在这台浏览器里。清除站点数据、更换设备或卸载浏览器都会导致本地录音丢失，且无法从云端恢复。',
            '你必须确保有权录制当场的所有说话人，并遵守所在地关于监听、课堂录音和同意的法律。我们无法替你取得第三方同意。',
          ],
        },
      ],
    },
    {
      id: 'use',
      heading: '5. 我们如何使用信息',
      blocks: [
        {
          type: 'p',
          text: '我们出于以下目的使用信息：',
        },
        {
          type: 'ul',
          items: [
            '提供转录、翻译、历史、导出、云端同步、AI 助手、知识库和学习空间。',
            '创建和维护账户，发送验证邮件，鉴权与会话隔离。',
            '计量用量、预留与结算费用，处理充值和会员，防止滥用。',
            '保障服务安全与稳定，诊断故障，执行配额和频率限制。',
            '在你主动发起时生成摘要、笔记、行动项、问答和练习材料。',
            '遵守法律义务，处理争议，执行用户条款。',
          ],
        },
        {
          type: 'p',
          text: '我们不会把你的录音、转录或知识库出售给广告商，也不会用它们投放第三方广告。默认情况下，仅仅开始录音不会触发计费的语义索引或后台摘要；这些需要你明确操作或开启相应选项。',
        },
      ],
    },
    {
      id: 'share',
      heading: '6. 我们与谁共享信息',
      blocks: [
        {
          type: 'p',
          text: '我们不会出售你的个人信息。为提供服务，我们会向以下服务商披露必要的数据。除本节末尾另有说明外，它们均作为处理者、仅按我们的指示处理数据：',
        },
        {
          type: 'ul',
          items: [
            '语音识别服务商（官方路径为 Speechmatics）：实时或批量音频，以及为识别所需的语言等配置。当前实时接口位于欧洲。该服务商同时会将这些数据用于改进其自身模型，详见本节末尾。',
            '大模型 / 翻译接口（由我们或部署运营方配置的 OpenAI 兼容服务）：你主动发送的文本、提示词、检索块和生成指令。Speechmatics 内置机器翻译如被选用，则会把相应文本交给该服务。',
            '支付处理商 Stripe：为完成结账、订阅、退款和税务所需的账户与交易信息。',
            '邮件发送服务（Resend 或运营方配置的 SMTP）：邮箱地址和验证 / 通知内容。',
            '基础设施提供方：例如托管、数据库、对象存储、内容分发和反向代理，它们可能处理运维所必需的技术数据。',
          ],
        },
        {
          type: 'p',
          text: '关于模型训练：我们在语音识别服务商 Speechmatics 的账户目前开启了「Model Training」设置，以换取更低的转录单价。依据其《Model Training Agreement》，经该服务处理的音频与转录（其条款所称「Your Data」，包含转录输出）会被 Speechmatics 用于改进其模型与服务，并可能为此披露给其关联公司、员工、承包商及其他第三方；就该用途而言，Speechmatics 以独立控制者的身份处理其中的个人数据，适用其自身的隐私政策，而非仅按我们的指示行事。Speechmatics 将该计划描述为使用「匿名化数据」；我们无法核实其匿名化到何种程度，且同一份协议已明文承认「Your Data」中含有个人数据、并据此约定了控制者身份，因此本政策不把这些数据当作已匿名处理。这一点是我们的选择，不是使用转录技术的必然结果。',
        },
        {
          type: 'p',
          text: '与此相对：我们自己不会用你的音频、转录或笔记训练任何模型，Stripe、邮件与基础设施服务商也只作为处理者按我们的指示处理数据。若你不希望自己的音频与转录进入上述训练计划，请来信 support@coyume.com；在我们提供逐账号的选择之前，唯一可靠的办法是不使用实时与批量转录功能。若我们日后关闭该设置或改变其范围，会在本页更新并注明生效日期。',
        },
        {
          type: 'p',
          text: '若法律要求，或为保护人身、财产与服务安全所必需，我们也可能披露信息。浏览器扩展在同步课程材料时，只向 Yufolo 服务器上传派生文本（以及用于即时 OCR 的临时渲染图，识别后丢弃）；扩展不会把 Moodle 登录 Cookie 或令牌发送给我们。',
        },
      ],
    },
    {
      id: 'overseas',
      heading: '7. 跨境处理',
      blocks: [
        {
          type: 'p',
          text: '我们是澳大利亚公司。为提供实时识别、AI、支付和邮件，你的信息可能被处理于澳大利亚以外，包括欧洲（语音识别）、美国或其他你或运营方选择的模型 / 支付服务所在地。',
        },
        {
          type: 'p',
          text: '使用本服务，即表示你理解这些处理可能发生在你居住地以外，当地的数据保护法律可能不同。我们仅向提供服务所必需的处理者发送数据，并要求他们按合同和各自政策保护数据。欧盟与英国用户适用的传输机制见第 11 节。',
        },
      ],
    },
    {
      id: 'storage',
      heading: '8. 浏览器本地存储与 Cookie',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo 主要使用浏览器存储，而不是广告 Cookie：',
        },
        {
          type: 'ul',
          items: [
            '注册安全 Cookie：服务端签名的随机浏览器标识（30 天），用于识别重复领取注册赠送权益，不跨站追踪。注册时还会读取 UA、平台、语言、时区、屏幕尺寸档位、硬件并发数、触点数及自动化标志，用于与网络来源结合评估重复领取风险；不使用 Canvas、音频或字体探测。',
            'localStorage：登录令牌、界面语言、非密钥设置、引导状态、有限的聊天记录。',
            'sessionStorage：标签页级的第三方 API 密钥（若管理员允许）以及少量进行中的任务状态。',
            'IndexedDB：会话元数据、转录、可选本地录音分块、待同步的云端写入队列。登录后按账号隔离；访客数据与登录数据分开。',
          ],
        },
        {
          type: 'p',
          text: '这些存储是提供登录态、长会话和离线重试所必需的。你可以在浏览器中清除站点数据，但这会删除本机录音、未同步队列和本地历史。我们目前不使用第三方广告追踪或跨站营销 Cookie。',
        },
      ],
    },
    {
      id: 'retention',
      heading: '9. 保存期限',
      blocks: [
        {
          type: 'ul',
          items: [
            '账户：在账户存续期间保存；删除账户后，除非法律要求保留，我们将删除或去标识化账户资料。',
            '云端会话文字：保存至你删除该会话，或注销账户为止。删除会话会同时删除对应的云端转录副本。我们目前不会按时间自动清理历史会话。',
            '本地录音与缓存：直到你删除会话、清除站点数据，或浏览器回收存储。',
            '计费账本与发票相关记录：为财务、税务和争议处理，在适用法律要求的期限内保留。',
            '验证邮件令牌：短期有效（当前为 24 小时）。',
            '安全与访问日志：保留为诊断和滥用防范所需的合理期限。注册风控保存浏览器标识、浏览器特征组合及网络地址/网段的带密钥摘要，以及粗略浏览器/平台类别、规则命中与关联计数，满 30 天后由每日任务清除这些关联标识；保留审核记录和规范化邮箱的带密钥摘要以防删除账号后重复领取赠送权益。',
            '知识文件与向量索引：直到你删除对应项目、来源或账户。',
          ],
        },
      ],
    },
    {
      id: 'rights',
      heading: '10. 你的权利',
      blocks: [
        {
          type: 'p',
          text: '在适用法律允许的范围内，你可以：',
        },
        {
          type: 'ul',
          items: [
            '查阅和更正账户资料（工作区内的账户与个人资料）。',
            '导出转录与译文（原文、译文或双语文本）；在本机保存了录音时，也可从该设备下载音频。',
            '删除单个会话；删除后云端文字副本一并删除，本机录音从未上传故无法从云端抹除（请在本机删除或清除站点数据）。',
            '撤回非必要处理：例如关闭本地录音、关闭自动 AI 入库、停止使用 AI 功能。实时转录本身依赖语音处理，无法在继续使用该功能的同时完全撤回。',
            '请求删除整个账户：当前产品未提供自助注销，请从注册邮箱发信至 support@coyume.com，我们会人工处理。',
          ],
        },
        {
          type: 'p',
          text: '澳大利亚用户可依据《1988 年隐私法》及澳大利亚隐私原则行使权利。若你对我们的处理不满意，可向 Office of the Australian Information Commissioner（OAIC）投诉。其他司法辖区的用户也可按当地法律提出请求；我们将在核实身份后合理期限内答复。',
        },
      ],
    },
    {
      id: 'gdpr',
      heading: '11. 欧盟与英国用户',
      blocks: [
        {
          type: 'p',
          text: '若你在欧洲经济区、瑞士或英国境内使用 Yufolo，欧盟《通用数据保护条例》（GDPR）及英国版 GDPR 适用于我们对你个人数据的处理。本节是对上文的补充，如与上文冲突，以本节为准。就本服务而言，Coyume Pty Ltd 是控制者；自行部署的实例由该部署的运营方担任控制者。',
        },
        {
          type: 'p',
          text: '我们处理你的个人数据依据以下法律基础：',
        },
        {
          type: 'ul',
          items: [
            '履行合同（第 6(1)(b) 条）：账户与鉴权、实时与批量转录、翻译、云端文字同步、导出、学习空间、用量计量与结算。不做这些处理就无法向你交付服务。',
            '你的同意（第 6(1)(a) 条）：麦克风与系统音频采集、可选的本地录音保存、由你发起的 AI 生成与语义索引。你可以随时撤回同意，撤回不影响撤回前处理的合法性。',
            '合法利益（第 6(1)(f) 条）：服务安全、滥用与欺诈防范、故障诊断、配额与频率限制。我们已就此做过利益衡量，你可以随时提出反对（见下）。',
            '法律义务（第 6(1)(c) 条）：财务、税务与争议处理所需的记录保存。',
          ],
        },
        {
          type: 'p',
          text: '特殊类别数据（第 9 条）：录音可能无意中包含健康、宗教信仰、政治观点等特殊类别信息。我们不会主动识别、提取或利用这类信息，也不会据此对你做画像。若你计划在可能涉及此类内容的场合录音，取得在场人员的明示同意（第 9(2)(a) 条）是你的责任。',
        },
        {
          type: 'p',
          text: '第二个控制者：如第 6 节所述，语音识别服务商 Speechmatics 会将经其处理的音频与转录用于改进自身模型，并就该用途以独立控制者身份行事。我们向其披露的法律基础是你的同意（第 6(1)(a) 条）：创建账户时你需要主动勾选并接受本政策，未勾选无法完成注册，本节即为该同意所涵盖的说明。你可以随时停止使用转录功能以撤回同意，撤回不影响撤回前处理的合法性；若你希望我们一并删除已同步的转录，请来信 support@coyume.com。对于 Speechmatics 以控制者身份进行的处理，我们无法代你行使或限制，你的查阅、删除与反对等权利需直接向 Speechmatics 主张，适用其隐私政策。若录音中包含其他在场人员的语音，就其个人数据取得合法依据是你的责任。',
        },
        {
          type: 'p',
          text: '跨境传输：我们位于澳大利亚，语音识别、模型接口、支付与邮件服务商也可能位于欧洲经济区以外。对这类传输，我们依赖与各处理者签订的数据处理协议中所载的欧盟委员会标准合同条款（SCC）及英国国际数据传输附录，并在必要时评估补充保障措施。你可以来信索取我们所依赖机制的说明。',
        },
        {
          type: 'p',
          text: '除第 10 节列出的权利外，GDPR 还赋予你以下权利：',
        },
        {
          type: 'ul',
          items: [
            '查阅权、更正权与删除权（「被遗忘权」）。',
            '限制处理权，以及对基于合法利益的处理提出反对的权利。',
            '数据可携带权：以结构化、通用、机器可读的格式取得你提供给我们的数据。工作区的导出功能已可导出转录与译文文本。',
            '撤回同意的权利，且不影响撤回前处理的合法性。',
            '不受仅基于自动化处理（含画像）、并对你产生法律效果或类似重大影响的决定约束。我们目前不做这类决定；AI 生成的摘要、评分与练习建议仅供你参考，不会自动决定你的权利或待遇。',
          ],
        },
        {
          type: 'p',
          text: '行使上述权利请发信至 support@coyume.com。我们会在收到请求后一个月内答复；请求复杂或数量较多时，我们可延长最多两个月，并会在最初一个月内告知你延期及其原因。你也有权向你惯常居住地、工作地或涉嫌违规发生地的数据保护监管机构投诉；英国用户可向 Information Commissioner’s Office（ICO）投诉。',
        },
      ],
    },
    {
      id: 'children',
      heading: '12. 儿童',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo 面向 18 岁以上的用户。我们不故意收集儿童的个人信息。若你认为我们持有儿童的信息，请联系 support@coyume.com，我们会删除。课堂场景下，由成年账户持有人负责取得学生或监护人的必要同意，并遵守所在学校与当地法律。',
        },
      ],
    },
    {
      id: 'security',
      heading: '13. 安全',
      blocks: [
        {
          type: 'p',
          text: '我们采取与服务性质相符的措施，包括密码哈希、JWT 鉴权、按账号隔离的本地与云端数据、传输加密（HTTPS / WSS）、接口限流，以及将密钥保留在服务端。没有任何系统是绝对安全的。请使用强密码，不要把验证链接转给他人，也不要在不信任的设备上开启本地录音后离开。',
        },
      ],
    },
    {
      id: 'selfhost',
      heading: '14. 自行部署',
      blocks: [
        {
          type: 'p',
          text: 'DreamTrans 可以自行部署。自行部署时，部署运营方决定数据库、密钥、模型供应商、邮件和支付配置，并成为该实例的个人信息处理者。本政策描述的是软件默认数据流和官方 Yufolo 服务；自行部署的运营方应发布自己的隐私政策，并对其用户负责。',
        },
      ],
    },
    {
      id: 'changes',
      heading: '15. 政策变更',
      blocks: [
        {
          type: 'p',
          text: '我们可能更新本政策。更新后的版本会在本页面公布，并修改生效日期。若变更实质影响你的权利，我们会通过网站提示或发给注册邮箱的邮件合理通知。继续使用服务即表示你接受更新后的政策。',
        },
      ],
    },
    {
      id: 'contact',
      heading: '16. 如何联系我们',
      blocks: [
        {
          type: 'p',
          text: '隐私请求、投诉与删除请求请发送至 support@coyume.com，并说明你的请求类型（查阅、更正、导出、删除账户等）；从注册邮箱发出可以帮助我们核实身份。官方 Yufolo 服务由澳大利亚公司 Coyume Pty Ltd 运营。若你对我们的答复不满意，可按第 10 节所述向 OAIC 投诉。',
        },
      ],
    },
  ],
}

const termsZh: LegalDocument = {
  kind: 'terms',
  title: '用户条款',
  summary:
    '本条款构成你与 Coyume Pty Ltd 之间关于使用 Yufolo 的协议。创建账户、登录或使用服务，即表示你同意本条款和《隐私政策》。若你代表组织使用服务，你保证有权使该组织受本条款约束。',
  sections: [
    {
      id: 'accept',
      heading: '1. 接受条款',
      blocks: [
        {
          type: 'p',
          text: '你必须年满 18 周岁，并具有签订合同的行为能力。若你不同意本条款，请不要注册或使用服务。',
        },
        {
          type: 'p',
          text: '自行部署 DreamTrans 的运营方与其用户之间的合同由该运营方自行制定；本条款适用于 Coyume Pty Ltd 运营的官方 Yufolo 服务，并作为软件默认产品规则的说明。',
        },
      ],
    },
    {
      id: 'service',
      heading: '2. 服务说明',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo 提供实时与批量语音转录、翻译、会话历史、导出、可选的云端文字同步、AI 辅助（问答、摘要、笔记、行动项、知识库）以及学习空间。功能是否可用取决于部署配置、你的套餐、余额和网络状况。',
        },
        {
          type: 'p',
          text: '服务依赖第三方语音识别、模型接口和支付基础设施。我们不保证某一第三方持续可用，也不保证识别或翻译结果完全准确。',
        },
      ],
    },
    {
      id: 'account',
      heading: '3. 账户',
      blocks: [
        {
          type: 'ul',
          items: [
            '你须提供真实、可用的邮箱，并完成验证（在要求验证的部署上，未验证账户不能登录，也不会获得试用额度）。',
            '你负责保管密码和验证链接。通过你的账户进行的活动视为你的行为。',
            '一个邮箱只能注册一个账户。我们可能拒绝一次性邮箱或策略不允许的域名。',
            '我们可在有合理理由认为账户被滥用、欠费或违反本条款时暂停或终止账户。',
          ],
        },
      ],
    },
    {
      id: 'recording',
      heading: '4. 录音、同意与合法使用',
      blocks: [
        {
          type: 'p',
          text: '你对录音内容和你提交的全部材料承担法律责任。你保证并承诺：',
        },
        {
          type: 'ul',
          items: [
            '你已获得录制、转录、翻译和存储当场所有说话人所必需的同意、许可或法律规定的其他依据。',
            '你不会使用服务侵犯他人隐私、窃听、未经授权监控，或违反学校、雇主、会场或所在地的录音规则。',
            '你不会上传或生成违法、侵权、骚扰、或试图破解服务的内容。',
            '你不会绕过计费、配额、鉴权或技术限制，不会共享账户以规避套餐限制。',
            '课堂、会议或公共场合使用时，由你负责告知参与者并遵守当地法律；Yufolo 不是你的合规官。',
          ],
        },
      ],
    },
    {
      id: 'content',
      heading: '5. 你的内容',
      blocks: [
        {
          type: 'p',
          text: '你保留对你的音频、转录、译文、上传文件、笔记和学习材料（「用户内容」）的权利。为向你提供服务，你授予我们一项非独占、全球范围、可向处理者再许可的许可：仅为运营、维护、安全保护和改进向你提供的功能而处理用户内容。',
        },
        {
          type: 'p',
          text: '我们不会主张对你的课堂录音或笔记的版权，我们自己也不会用用户内容训练模型。但请注意：上述再许可确实包含把音频与转录交给语音识别服务商，而该服务商会将其用于改进自身模型——具体范围见隐私政策第 6 节，请在使用转录功能前阅读。你可以随时导出文字；删除会话即删除对应的云端文字副本。本地录音只存在于你的设备。',
        },
      ],
    },
    {
      id: 'ai',
      heading: '6. AI 功能',
      blocks: [
        {
          type: 'p',
          text: '翻译、问答、摘要、笔记、行动项、技能地图和练习题由自动化模型生成，可能不准确、不完整或过时。它们不是法律、医疗、学术或专业建议，不能替代你自己的判断。你应对采用任何 AI 输出负责。',
        },
        {
          type: 'p',
          text: '为控制费用，语义索引和多数生成任务需要你明确发起或确认。预览接口在默认情况下可以只做不计费的词法预览；只有你要求执行语义检索时才会产生相应费用。',
        },
      ],
    },
    {
      id: 'fees',
      heading: '7. 费用、钱包与订阅',
      blocks: [
        {
          type: 'ul',
          items: [
            '计费账本以美元记账。转录按音频时长（由转发的音频字节折算）、AI 按 token 或约定规则计费。每次调用可先按估算上限预留，完成后按真实用量结算并退回差额。',
            '赠送额度（含注册试用）可能有有效期；钱包充值余额在账户存续期间通常不过期，但不构成可兑现现金。',
            '会员（付费套餐）提供折扣、功能解锁和更高限额，不包含无限小时数。订阅按页面展示的周期收费，可按 Stripe 客户门户或产品内说明取消；取消后通常在当前周期结束前仍可使用已付权益。',
            '已消耗的用量不予退款。未使用的充值或订阅是否退还，按适用的消费者保护法律及我们届时公布的退款安排处理。',
            '价格、加价、套餐和充值档位可由运营方调整；对已完成的结算，以当时有效的价格为准。',
            '若余额不足，相关功能可能被拒绝（例如返回支付所需错误），直到你充值或获得额度。',
          ],
        },
        {
          type: 'p',
          text: '澳大利亚消费者法赋予的不可排除权利不受本条款限制。在该法律适用的范围内，我们对未能遵守消费者保证的救济以法律允许的更换、再提供服务或支付同等费用为限。',
        },
      ],
    },
    {
      id: 'ip',
      heading: '8. 我们的知识产权',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo / DreamTrans 的软件、界面、商标、文案和文档归 Coyume Pty Ltd 或其许可方所有。软件按 PolyForm Noncommercial License 1.0.0 及仓库中的许可文件提供；未经许可的商业使用可能构成侵权。本条款授予你一份有限的、不可转让的、可撤销的使用官方服务的许可，不转让任何知识产权。',
        },
      ],
    },
    {
      id: 'thirdparty',
      heading: '9. 第三方服务',
      blocks: [
        {
          type: 'p',
          text: '语音识别、模型接口、支付和邮件由第三方提供。你与这些第三方之间还可能受其条款约束。因第三方中断、拒绝服务、价格变化或政策变化导致的功能不可用，我们在法律允许范围内不承担责任，但会合理努力告知受影响的功能。',
        },
      ],
    },
    {
      id: 'disclaimer',
      heading: '10. 免责声明',
      blocks: [
        {
          type: 'p',
          text: '在适用法律允许的范围内，服务按「现状」和「可用」提供。我们不保证服务不中断、无错误，也不保证转录、翻译或 AI 输出适合某一特定用途。实时识别依赖网络和第三方；断网时本机可能仍保存已确认的文字，但不等于可以离线继续识别。',
        },
      ],
    },
    {
      id: 'liability',
      heading: '11. 责任限制',
      blocks: [
        {
          type: 'p',
          text: '在适用法律允许的范围内，我们对间接损失、利润损失、数据丢失、商誉损失或替代服务费用不承担责任。我们对你在任一自然月内的合计责任，不超过你在该月就相关服务向我们实际支付的费用（不含尚未消耗的钱包余额）。',
        },
        {
          type: 'p',
          text: '你须就因你违反本条款、非法录音或侵犯第三方权利而导致的索赔，向我们赔偿并使我们免受损害。',
        },
      ],
    },
    {
      id: 'term',
      heading: '12. 期限与终止',
      blocks: [
        {
          type: 'p',
          text: '你可随时停止使用服务并删除会话。账户删除请发信至 support@coyume.com。我们可因维护、安全、违法或严重违约立即暂停服务。条款中依其性质应继续有效的部分（包括费用、知识产权、免责和责任限制）在终止后仍然有效。',
        },
      ],
    },
    {
      id: 'changes',
      heading: '13. 条款变更',
      blocks: [
        {
          type: 'p',
          text: '我们可能更新本条款，并在本页面公布新版本。若变更实质影响你的权利或费用结构，我们会通过网站或邮件合理通知。在生效日期之后继续使用服务，即表示接受更新后的条款。',
        },
      ],
    },
    {
      id: 'law',
      heading: '14. 适用法律',
      blocks: [
        {
          type: 'p',
          text: '本条款受澳大利亚法律管辖。在消费者强制管辖规则允许的范围内，双方接受澳大利亚法院的管辖。若某一条款被认定为不可执行，其余部分仍然有效。',
        },
      ],
    },
    {
      id: 'contact',
      heading: '15. 联系我们',
      blocks: [
        {
          type: 'p',
          text: '关于本条款的问题，请发送至 support@coyume.com。官方 Yufolo 服务由澳大利亚公司 Coyume Pty Ltd 运营。',
        },
      ],
    },
  ],
}

const privacyEn: LegalDocument = {
  kind: 'privacy',
  title: 'Privacy Policy',
  summary:
    'This policy explains how Yufolo (the customer-facing name of the DreamTrans software) collects, uses, stores and shares your information. It follows the product’s actual data flows and applies to the official hosted service operated by Coyume Pty Ltd. If you use a self-hosted instance, that operator is the controller for that instance and may publish different terms.',
  sections: [
    {
      id: 'who',
      heading: '1. Who we are',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo is operated by Coyume Pty Ltd, an Australian proprietary company (“we”). Yufolo provides live speech transcription, translation, session history, AI assistance and a study workspace. The engineering name of the software is DreamTrans.',
        },
        {
          type: 'p',
          text: 'Privacy requests can be sent to support@coyume.com. Writing from the address on your account helps us verify you faster.',
        },
      ],
    },
    {
      id: 'scope',
      heading: '2. Scope',
      blocks: [
        {
          type: 'p',
          text: 'This policy covers the website, workspace, admin dashboard, study space, and official browser extensions (such as Moodle sync) when they talk to a Yufolo server.',
        },
        {
          type: 'p',
          text: 'It does not cover the independent processing of speech-recognition, model, or payment providers — those services have their own privacy policies. The main processors are listed below.',
        },
      ],
    },
    {
      id: 'collect',
      heading: '3. Information we collect',
      blocks: [
        {
          type: 'p',
          text: 'We collect what we need to run the service, including:',
        },
        {
          type: 'ul',
          items: [
            'Account data: email, display name, password hash, role, tenant, verification status. We do not store plaintext passwords.',
            'Audio: after you grant permission, the browser captures microphone and/or system/tab audio. Live audio is forwarded through our servers to the speech-recognition provider for transcription and byte-based metering. Cloud session sync does not upload or keep a full recording. If you enable local audio saving, chunks are written only to IndexedDB on that device.',
            'Batch files: audio you submit for batch transcription is passed through our servers to the speech-recognition provider. We do not keep the file ourselves; how long it stays with the provider is governed by that provider’s data retention terms, and the provider uses it to improve its own models (see section 6).',
            'Transcripts and translations: speaker-attributed text, timestamps and related metadata. Guest mode keeps this mainly on-device; signed-in sessions sync text to our database and keep an account-scoped browser cache.',
            'Knowledge and study content: files you upload, editable memories, summaries, notes, action items, skill maps, practice records, and derived text synced from a learning system.',
            'Billing data: usage, reservations and settlements, wallet and grant balances, plans and top-ups. Card details are handled by Stripe; we do not store full card numbers.',
            'Signup reward eligibility: risk signals from repeat browser registrations, network registration frequency and normalized email history may delay free rewards for administrator review. Account access remains available; contact support@coyume.com to request review.',
            'Technical and security logs: IP address, user agent, request path, errors and latency, Cloudflare Ray ID when present, and limited connection data used for sign-up rate limits.',
            'Settings you choose: interface language, source/target languages, translation engine and prompts, audio-input preferences. When an administrator allows a bring-your-own API key, that secret is kept only in this tab’s sessionStorage or memory and is cleared on logout or when the tab closes.',
          ],
        },
      ],
    },
    {
      id: 'audio',
      heading: '4. How audio is handled',
      blocks: [
        {
          type: 'p',
          text: 'Speech is core to Yufolo. By starting a recording you understand and agree that:',
        },
        {
          type: 'ul',
          items: [
            'Live audio is streamed through our servers to the speech-recognition provider (the official path uses Speechmatics’ European real-time endpoint) so we can produce text and meter forwarded audio bytes.',
            'We do not store or sync the full recording as part of a cloud session. When the product says audio stays on your device, it means Yufolo’s cloud copy is text, not a downloadable cloud soundtrack.',
            'Local recordings are optional, enabled in settings, and exist only in that browser. Clearing site data, switching devices or removing the browser deletes them; they cannot be restored from the cloud.',
            'You must have the right to record everyone present and must follow local laws on listening, classroom recording and consent. We cannot obtain third-party consent for you.',
          ],
        },
      ],
    },
    {
      id: 'use',
      heading: '5. How we use information',
      blocks: [
        {
          type: 'p',
          text: 'We use information to:',
        },
        {
          type: 'ul',
          items: [
            'Provide transcription, translation, history, export, cloud text sync, the AI assistant, knowledge projects and the study space.',
            'Create and maintain accounts, send verification email, authenticate and isolate sessions.',
            'Meter usage, reserve and settle charges, process top-ups and memberships, and prevent abuse.',
            'Keep the service secure and reliable, diagnose faults, and enforce quotas and rate limits.',
            'Generate summaries, notes, action items, answers and practice material when you ask.',
            'Meet legal duties, handle disputes and enforce the Terms of Use.',
          ],
        },
        {
          type: 'p',
          text: 'We do not sell your recordings, transcripts or knowledge base to advertisers, and we do not use them for third-party ads. Starting a recording does not, by itself, start a paid semantic index or background summary; those need an explicit action or a setting you turn on.',
        },
      ],
    },
    {
      id: 'share',
      heading: '6. Who we share information with',
      blocks: [
        {
          type: 'p',
          text: 'We do not sell your personal information. We disclose what is needed to the providers below. Except where the end of this section says otherwise, they act as processors on our instructions only:',
        },
        {
          type: 'ul',
          items: [
            'Speech-recognition provider (official path: Speechmatics): live or batch audio and recognition settings. The current real-time endpoint is in Europe. This provider also uses that data to improve its own models — see the end of this section.',
            'Model / translation APIs (OpenAI-compatible services configured by us or the deployment operator): text, prompts, retrieved chunks and generation instructions you send. If you choose Speechmatics machine translation, that text is sent there instead.',
            'Stripe: account and transaction data needed for checkout, subscriptions, refunds and tax.',
            'Email delivery (Resend or operator-configured SMTP): your address and verification or notice content.',
            'Infrastructure providers such as hosting, databases, object storage, CDNs and reverse proxies, which may process technical data required to operate the service.',
          ],
        },
        {
          type: 'p',
          text: 'On model training: our account with the speech-recognition provider, Speechmatics, currently has its “Model Training” setting switched on, in exchange for a lower per-minute price. Under their Model Training Agreement, the audio and transcripts processed through that service (their terms call this “Your Data”, and it includes transcribed output) are used by Speechmatics to improve their models and services, and may be disclosed to their affiliates, employees, contractors and other third parties for that purpose. For that use Speechmatics processes the personal data in it as an independent controller under its own privacy policy, not solely on our instructions. Speechmatics describes the programme as using “anonymized data”; we cannot verify how far that goes, and the same agreement expressly acknowledges that Your Data contains personal data and assigns controller responsibility on that basis, so this policy does not treat it as anonymised. This is our choice, not an unavoidable part of using speech recognition.',
        },
        {
          type: 'p',
          text: 'By contrast: we do not train any model of our own on your audio, transcripts or notes, and Stripe, our email provider and our infrastructure providers act as processors on our instructions only. If you would rather your audio and transcripts stayed out of that training programme, write to support@coyume.com; until we offer a per-account choice, the only reliable option is not to use live or batch transcription. If we later switch the setting off or change its scope, we will update this page and date the change.',
        },
        {
          type: 'p',
          text: 'We may also disclose information if the law requires it, or if it is necessary to protect people, property or the service. The browser extension uploads derived page text to Yufolo (and a temporary render used for on-the-spot OCR, which is discarded afterwards). It does not send Moodle session cookies or tokens to us.',
        },
      ],
    },
    {
      id: 'overseas',
      heading: '7. Overseas processing',
      blocks: [
        {
          type: 'p',
          text: 'We are an Australian company. To provide recognition, AI, payments and email, your information may be processed outside Australia, including Europe (speech recognition), the United States, or another region chosen for the model or payment provider you or the operator configure.',
        },
        {
          type: 'p',
          text: 'By using the service you understand that processing may occur outside your country, where privacy laws can differ. We send data only to processors needed to provide the service and require them to protect it under contract and their own policies. The transfer mechanism for EU and UK users is described in section 11.',
        },
      ],
    },
    {
      id: 'storage',
      heading: '8. Local browser storage and cookies',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo relies on browser storage rather than advertising cookies:',
        },
        {
          type: 'ul',
          items: [
            'Signup security cookie: a server-signed random browser identifier (30 days) used to detect repeated signup rewards without cross-site tracking. At signup we also read the user agent, platform, language, time zone, coarse screen dimensions, hardware concurrency, touch-point count and automation flag to assess repeated rewards together with network signals. We do not probe canvas, audio or fonts.',
            'localStorage: sign-in tokens, interface language, non-secret settings, onboarding state and a limited chat history.',
            'sessionStorage: a tab-scoped third-party API key when allowed, and a little in-flight task state.',
            'IndexedDB: session metadata, transcripts, optional local audio chunks, and the pending cloud-write outbox. Signed-in data is account-scoped; guest data stays separate.',
          ],
        },
        {
          type: 'p',
          text: 'This storage is required for sign-in, long sessions and offline retries. You can clear site data in the browser; that also deletes local recordings, unsynced writes and local history. We do not currently use third-party advertising or cross-site marketing cookies.',
        },
      ],
    },
    {
      id: 'retention',
      heading: '9. Retention',
      blocks: [
        {
          type: 'ul',
          items: [
            'Accounts: kept while the account exists; after deletion we delete or de-identify account data unless the law requires us to keep it.',
            'Cloud session text: kept until you delete the session or close the account. Deleting a session deletes the matching cloud transcript. We do not currently purge old sessions on a timer.',
            'Local recordings and caches: until you delete the session, clear site data, or the browser reclaims storage.',
            'Billing ledgers and invoice-related records: kept for the period required for finance, tax and disputes.',
            'Email verification tokens: short-lived (currently 24 hours).',
            'Security and access logs: kept for a reasonable period for diagnosis and abuse prevention. Signup risk stores keyed hashes of browser identifiers, browser characteristic combinations and network addresses/prefixes, along with coarse browser/platform categories, rule matches and correlation counts, clearing those correlation identifiers in a daily job after 30 days. Review records and keyed hashes of normalized emails remain to prevent repeated rewards after account deletion.',
            'Knowledge files and vector indexes: until you delete the project, source or account.',
          ],
        },
      ],
    },
    {
      id: 'rights',
      heading: '10. Your rights',
      blocks: [
        {
          type: 'p',
          text: 'Where applicable law allows, you may:',
        },
        {
          type: 'ul',
          items: [
            'Access and correct account details in the workspace.',
            'Export transcripts and translations (original, translated or bilingual text) and, if a local recording exists on that device, download the audio.',
            'Delete individual sessions. That removes the cloud text copy. Local audio was never uploaded, so delete it on the device or clear site data.',
            'Withdraw optional processing: turn off local audio saving, turn off automatic AI ingest, or stop using AI features. Live transcription itself requires speech processing and cannot be fully withdrawn while you keep using that feature.',
            'Ask us to delete the whole account. There is no self-serve close-account button today; write to support@coyume.com from your registered email and we will handle it.',
          ],
        },
        {
          type: 'p',
          text: 'People in Australia may exercise rights under the Privacy Act 1988 (Cth) and the Australian Privacy Principles. If you are not satisfied, you may complain to the Office of the Australian Information Commissioner (OAIC). Users elsewhere may make requests under local law. We will respond within a reasonable time after verifying your identity.',
        },
      ],
    },
    {
      id: 'gdpr',
      heading: '11. Users in the EU and UK',
      blocks: [
        {
          type: 'p',
          text: 'If you use Yufolo in the European Economic Area, Switzerland or the United Kingdom, the EU General Data Protection Regulation and the UK GDPR apply to our processing of your personal data. This section adds to the sections above and prevails where they conflict. For this service Coyume Pty Ltd is the controller; for a self-hosted instance, that deployment’s operator is.',
        },
        {
          type: 'p',
          text: 'We rely on these legal bases:',
        },
        {
          type: 'ul',
          items: [
            'Performance of a contract (Art. 6(1)(b)): accounts and authentication, live and batch transcription, translation, cloud text sync, export, the study space, and usage metering and settlement. Without this processing we cannot deliver the service you signed up for.',
            'Your consent (Art. 6(1)(a)): microphone and system audio capture, optional local recording, and AI generation or semantic indexing that you start. You can withdraw consent at any time; withdrawal does not affect processing carried out before it.',
            'Legitimate interests (Art. 6(1)(f)): service security, abuse and fraud prevention, fault diagnosis, quotas and rate limits. We have balanced those interests against your rights, and you may object at any time (below).',
            'Legal obligation (Art. 6(1)(c)): records we must keep for finance, tax and disputes.',
          ],
        },
        {
          type: 'p',
          text: 'Special category data (Art. 9): a recording may incidentally contain health, religious belief, political opinion or other special category information. We do not seek out, extract or exploit it, and we do not profile you on it. If you plan to record where such content is likely, obtaining explicit consent from the people present (Art. 9(2)(a)) is your responsibility.',
        },
        {
          type: 'p',
          text: 'A second controller: as section 6 explains, our speech-recognition provider, Speechmatics, uses the audio and transcripts it processes to improve its own models, and acts as an independent controller for that purpose. Our legal basis for disclosing it to them is your consent (Art. 6(1)(a)): creating an account requires you to tick a box accepting this policy, and registration cannot complete without it; this section is part of what that acceptance covers. You may withdraw by ceasing to use transcription, which does not affect the lawfulness of processing before then; write to support@coyume.com if you would also like the synced transcripts deleted. We cannot exercise or restrict their controller-side processing on your behalf: address access, erasure and objection requests about it to Speechmatics, under their privacy policy. Where a recording carries the voices of other people present, establishing a lawful basis for their personal data is your responsibility.',
        },
        {
          type: 'p',
          text: 'International transfers: we are in Australia, and the speech-recognition, model, payment and email providers may also sit outside the EEA. For those transfers we rely on the European Commission’s Standard Contractual Clauses and the UK International Data Transfer Addendum as incorporated in our data processing agreements with each processor, with supplementary measures where needed. Write to us for a description of the mechanism relied on.',
        },
        {
          type: 'p',
          text: 'Alongside the rights in section 10, the GDPR gives you:',
        },
        {
          type: 'ul',
          items: [
            'Rights of access, rectification and erasure (“the right to be forgotten”).',
            'The right to restrict processing, and to object to processing based on legitimate interests.',
            'Data portability: to receive the data you provided in a structured, commonly used, machine-readable format. The workspace already exports transcripts and translations as text.',
            'The right to withdraw consent, without affecting the lawfulness of processing before withdrawal.',
            'The right not to be subject to a decision based solely on automated processing, including profiling, that produces legal or similarly significant effects. We make no such decisions; AI summaries, scores and practice suggestions are for you to use and do not decide your rights or treatment.',
          ],
        },
        {
          type: 'p',
          text: 'To exercise these rights write to support@coyume.com. We answer within one month of receiving a request; where a request is complex or there are several, we may extend by up to two further months and will tell you why within the first month. You may also complain to the supervisory authority where you live, where you work, or where you think a breach occurred; in the UK that is the Information Commissioner’s Office (ICO).',
        },
      ],
    },
    {
      id: 'children',
      heading: '12. Children',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo is for users 18 and over. We do not knowingly collect children’s personal information. If you believe we hold a child’s information, contact support@coyume.com and we will delete it. In a classroom, the adult account holder is responsible for student or guardian consent and for school and local rules.',
        },
      ],
    },
    {
      id: 'security',
      heading: '13. Security',
      blocks: [
        {
          type: 'p',
          text: 'We use measures appropriate to the service, including password hashing, JWT authentication, account-scoped local and cloud data, HTTPS/WSS in transit, rate limits, and keeping provider keys on the server. No system is perfectly secure. Use a strong password, do not forward verification links, and do not leave local audio saving switched on when you walk away from a device you do not control.',
        },
      ],
    },
    {
      id: 'selfhost',
      heading: '14. Self-hosted deployments',
      blocks: [
        {
          type: 'p',
          text: 'DreamTrans can be self-hosted. The operator then chooses the database, secrets, model vendor, mail and payments, and is the controller for that instance. This policy describes default software data flows and the official Yufolo service. Self-hosted operators should publish their own privacy policy and are responsible to their users.',
        },
      ],
    },
    {
      id: 'changes',
      heading: '15. Changes',
      blocks: [
        {
          type: 'p',
          text: 'We may update this policy. The new version will be posted on this page with a new effective date. If a change materially affects your rights, we will give reasonable notice on the site or by email. Continued use after that date means you accept the updated policy.',
        },
      ],
    },
    {
      id: 'contact',
      heading: '16. Contact',
      blocks: [
        {
          type: 'p',
          text: 'Send privacy requests, complaints and deletion requests to support@coyume.com, and say what you want (access, correction, export, account deletion); writing from your registered email helps us verify you. The official Yufolo service is operated by Coyume Pty Ltd in Australia. If our answer does not satisfy you, you may complain to the OAIC as described in section 10.',
        },
      ],
    },
  ],
}

const termsEn: LegalDocument = {
  kind: 'terms',
  title: 'Terms of Use',
  summary:
    'These terms are the agreement between you and Coyume Pty Ltd for using Yufolo. Creating an account, signing in or using the service means you accept these terms and the Privacy Policy. If you use the service for an organisation, you confirm you can bind that organisation.',
  sections: [
    {
      id: 'accept',
      heading: '1. Acceptance',
      blocks: [
        {
          type: 'p',
          text: 'You must be 18 or older and able to enter a contract. If you do not agree, do not register or use the service.',
        },
        {
          type: 'p',
          text: 'A self-hosted DreamTrans operator sets the contract with its own users. These terms apply to the official Yufolo service operated by Coyume Pty Ltd, and describe the product’s default rules.',
        },
      ],
    },
    {
      id: 'service',
      heading: '2. The service',
      blocks: [
        {
          type: 'p',
          text: 'Yufolo provides live and batch speech transcription, translation, history, export, optional cloud text sync, AI assistance (chat, summaries, notes, action items, knowledge) and a study space. What you can use depends on the deployment, your plan, your balance and the network.',
        },
        {
          type: 'p',
          text: 'The service depends on third-party speech recognition, model APIs and payments. We do not guarantee that a given provider stays available, or that recognition or translation is perfectly accurate.',
        },
      ],
    },
    {
      id: 'account',
      heading: '3. Accounts',
      blocks: [
        {
          type: 'ul',
          items: [
            'You must give a real email address you can access and complete verification where the deployment requires it. On those deployments an unverified account cannot sign in and does not receive trial credit.',
            'You are responsible for your password and verification links. Activity through your account is treated as yours.',
            'One email may register one account. We may refuse disposable addresses or domains blocked by policy.',
            'We may suspend or close an account if we reasonably believe it is abused, unpaid or in breach of these terms.',
          ],
        },
      ],
    },
    {
      id: 'recording',
      heading: '4. Recording, consent and acceptable use',
      blocks: [
        {
          type: 'p',
          text: 'You are legally responsible for what you record and upload. You warrant that:',
        },
        {
          type: 'ul',
          items: [
            'You have all consents, licences or other lawful bases needed to record, transcribe, translate and store everyone present.',
            'You will not use the service to invade privacy, intercept communications without authority, or break school, employer, venue or local recording rules.',
            'You will not upload or generate unlawful, infringing, harassing or attack content, or attempt to break the service.',
            'You will not bypass billing, quotas, authentication or technical limits, or share an account to evade plan limits.',
            'In a classroom, meeting or public setting you must inform participants and follow local law. Yufolo is not your compliance officer.',
          ],
        },
      ],
    },
    {
      id: 'content',
      heading: '5. Your content',
      blocks: [
        {
          type: 'p',
          text: 'You keep your rights in your audio, transcripts, translations, uploads, notes and study material (“User Content”). To provide the service you grant us a non-exclusive, worldwide licence, sublicensable to our processors, to process User Content only to operate, maintain, secure and improve the features we provide to you.',
        },
        {
          type: 'p',
          text: 'We do not claim copyright in your classroom recordings or notes, and we do not train models of our own on User Content. Note, though, that the sub-licence above does cover passing audio and transcripts to our speech-recognition provider, which uses them to improve its own models — section 6 of the Privacy Policy sets out the scope, and is worth reading before you use transcription. You may export text at any time. Deleting a session deletes the matching cloud text. Local audio lives only on your device.',
        },
      ],
    },
    {
      id: 'ai',
      heading: '6. AI features',
      blocks: [
        {
          type: 'p',
          text: 'Translation, answers, summaries, notes, action items, skill maps and practice questions are produced by automated models. They can be wrong, incomplete or stale. They are not legal, medical, academic or other professional advice and do not replace your judgement. You are responsible for any use of an AI output.',
        },
        {
          type: 'p',
          text: 'To keep costs explicit, semantic indexing and most generation tasks require you to start or confirm them. Context preview can run a free lexical preflight by default; a semantic query runs only when you ask for it, and is billed accordingly.',
        },
      ],
    },
    {
      id: 'fees',
      heading: '7. Fees, wallet and membership',
      blocks: [
        {
          type: 'ul',
          items: [
            'The ledger is kept in US dollars. Transcription is billed from forwarded audio bytes converted to duration; AI is billed by tokens or the published rule. A call may reserve an estimated maximum, then settle to actual use and refund the unused reserve.',
            'Grant credit (including a sign-up trial) may expire. Purchased wallet credit usually does not expire while the account exists, but it is not cash you can withdraw.',
            'Paid membership unlocks discounts, features and higher limits; it is not unlimited hours. Subscriptions bill on the cycle shown and can be cancelled through the Stripe customer portal or in-product instructions. Access already paid for usually lasts until the end of the current period.',
            'Consumed usage is not refundable. Whether unused credit or a subscription is refunded follows applicable consumer law and any refund practice we publish at the time.',
            'Prices, markup, plans and top-up tiers may change. Completed settlements use the price in force at the time.',
            'If the balance cannot cover a reservation, the feature may be refused until you add credit.',
          ],
        },
        {
          type: 'p',
          text: 'Nothing in these terms limits rights that cannot be excluded under the Australian Consumer Law. Where that law applies and cannot be excluded, our liability for a failure to meet a consumer guarantee is limited to resupplying the service or paying the cost of resupply, to the extent the law allows.',
        },
      ],
    },
    {
      id: 'ip',
      heading: '8. Our intellectual property',
      blocks: [
        {
          type: 'p',
          text: 'The Yufolo / DreamTrans software, interface, marks, copy and documentation belong to Coyume Pty Ltd or its licensors. The software is offered under the PolyForm Noncommercial License 1.0.0 and the licence files in the repository; unauthorised commercial use may be infringement. These terms give you a limited, non-transferable, revocable licence to use the official service. They do not transfer any intellectual property.',
        },
      ],
    },
    {
      id: 'thirdparty',
      heading: '9. Third-party services',
      blocks: [
        {
          type: 'p',
          text: 'Speech recognition, model APIs, payments and email are provided by third parties. You may also be bound by their terms. Within the limits of the law we are not liable for outages, refusals, price changes or policy changes at those providers, though we will take reasonable steps to tell you when a feature is affected.',
        },
      ],
    },
    {
      id: 'disclaimer',
      heading: '10. Disclaimer',
      blocks: [
        {
          type: 'p',
          text: 'To the extent permitted by law, the service is provided “as is” and “as available”. We do not warrant that it will be uninterrupted or error-free, or that transcription, translation or AI output is fit for a particular purpose. Live recognition needs the network and third parties. Confirmed text may remain on the device after a drop, but that is not offline recognition.',
        },
      ],
    },
    {
      id: 'liability',
      heading: '11. Limitation of liability',
      blocks: [
        {
          type: 'p',
          text: 'To the extent permitted by law we are not liable for indirect loss, lost profits, lost data, lost goodwill or the cost of substitute services. Our total liability to you in any calendar month is limited to the fees you actually paid us for the affected service in that month (excluding unused wallet credit).',
        },
        {
          type: 'p',
          text: 'You will indemnify us against claims arising from your breach of these terms, unlawful recording, or infringement of third-party rights.',
        },
      ],
    },
    {
      id: 'term',
      heading: '12. Term and termination',
      blocks: [
        {
          type: 'p',
          text: 'You may stop using the service and delete sessions at any time. Account deletion is handled on request — write to support@coyume.com. We may suspend the service immediately for maintenance, security, unlawfulness or a serious breach. Clauses that by their nature should survive (including fees, intellectual property, disclaimers and liability limits) continue after termination.',
        },
      ],
    },
    {
      id: 'changes',
      heading: '13. Changes',
      blocks: [
        {
          type: 'p',
          text: 'We may update these terms and post the new version on this page. If a change materially affects your rights or pricing, we will give reasonable notice on the site or by email. Using the service after the effective date means you accept the updated terms.',
        },
      ],
    },
    {
      id: 'law',
      heading: '14. Governing law',
      blocks: [
        {
          type: 'p',
          text: 'These terms are governed by the laws of Australia. Where mandatory consumer jurisdiction rules allow, the courts of Australia have jurisdiction. If one clause cannot be enforced, the rest remains in force.',
        },
      ],
    },
    {
      id: 'contact',
      heading: '15. Contact',
      blocks: [
        {
          type: 'p',
          text: 'Questions about these terms can be sent to support@coyume.com. The official Yufolo service is operated by Coyume Pty Ltd in Australia.',
        },
      ],
    },
  ],
}

const DOCUMENTS: Record<LegalKind, Record<Locale, LegalDocument>> = {
  privacy: { 'zh-CN': privacyZh, en: privacyEn },
  terms: { 'zh-CN': termsZh, en: termsEn },
}

export function legalDocument(kind: LegalKind, locale: Locale): LegalDocument {
  return DOCUMENTS[kind][locale]
}
