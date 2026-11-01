// Static landing page and logo for browsers that open the relay address. Nothing here reads state or
// calls the Durable Object, and it loads no external resource (strict CSP below).

/** The mark: a luminous core with six hypha filaments that fork twice and end in nodes (agents). Generated, static. */
export const LOGO_SVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256" role="img" aria-label="Hyphae"><defs><radialGradient id="bg" cx="50%" cy="45%" r="75%"><stop offset="0" stop-color="#14382f"/><stop offset="0.6" stop-color="#0b1a16"/><stop offset="1" stop-color="#050a08"/></radialGradient><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="30" y1="226" x2="226" y2="30"><stop offset="0" stop-color="#2dd4bf"/><stop offset="0.55" stop-color="#a78bfa"/><stop offset="1" stop-color="#fbbf24"/></linearGradient><radialGradient id="core"><stop offset="0" stop-color="#fff"/><stop offset="0.35" stop-color="#7ef0dc"/><stop offset="1" stop-color="#2dd4bf" stop-opacity="0"/></radialGradient><filter id="b" x="-30%" y="-30%" width="160%" height="160%"><feGaussianBlur stdDeviation="3.5"/></filter></defs><rect width="256" height="256" rx="56" fill="url(#bg)"/><g fill="none" stroke="#2dd4bf" stroke-opacity=".10"><circle cx="128" cy="128" r="62"/><circle cx="128" cy="128" r="94"/><circle cx="128" cy="128" r="118"/></g><g fill="none" stroke="url(#g)" stroke-linecap="round" filter="url(#b)" opacity=".7"><path stroke-width="6" d="M128 116Q128 92 133 72M138 122Q159 110 179 105M138 134Q159 146 174 160M128 140Q128 164 123 184M118 134Q97 146 77 151M118 122Q97 110 82 96"/><path stroke-width="4" d="M133 72Q127 57 125 44M133 72Q143 59 154 51M179 105Q189 92 200 83M179 105Q195 107 208 112M174 160Q190 162 203 168M174 160Q180 176 182 189M123 184Q129 199 131 212M123 184Q113 197 102 205M77 151Q67 164 56 173M77 151Q61 149 48 144M82 96Q66 94 53 88M82 96Q76 80 74 67"/><path stroke-width="2.4" d="M125 44Q118 35 114 27M125 44Q127 32 130 24M154 51Q158 40 163 32M154 51Q164 46 173 45M200 83Q203 73 209 65M200 83Q210 79 219 78M208 112Q219 110 228 110M208 112Q217 119 222 126M203 168Q214 166 223 166M203 168Q211 175 217 182M182 189Q189 198 193 206M182 189Q181 200 177 209M131 212Q138 221 142 229M131 212Q129 224 126 232M102 205Q98 216 93 224M102 205Q92 210 83 211M56 173Q53 183 47 191M56 173Q46 177 37 178M48 144Q37 146 28 146M48 144Q39 137 34 130M53 88Q42 90 33 90M53 88Q45 81 39 74M74 67Q67 58 63 50M74 67Q75 56 79 47"/></g><g fill="url(#g)" filter="url(#b)" opacity=".7"><circle cx="114" cy="27" r="2.8"/><circle cx="130" cy="24" r="2.8"/><circle cx="125" cy="44" r="3.2"/><circle cx="163" cy="32" r="2.8"/><circle cx="173" cy="45" r="2.8"/><circle cx="154" cy="51" r="3.2"/><circle cx="133" cy="72" r="4.6"/><circle cx="209" cy="65" r="2.8"/><circle cx="219" cy="78" r="2.8"/><circle cx="200" cy="83" r="3.2"/><circle cx="228" cy="110" r="2.8"/><circle cx="222" cy="126" r="2.8"/><circle cx="208" cy="112" r="3.2"/><circle cx="179" cy="105" r="4.6"/><circle cx="223" cy="166" r="2.8"/><circle cx="217" cy="182" r="2.8"/><circle cx="203" cy="168" r="3.2"/><circle cx="193" cy="206" r="2.8"/><circle cx="177" cy="209" r="2.8"/><circle cx="182" cy="189" r="3.2"/><circle cx="174" cy="160" r="4.6"/><circle cx="142" cy="229" r="2.8"/><circle cx="126" cy="232" r="2.8"/><circle cx="131" cy="212" r="3.2"/><circle cx="93" cy="224" r="2.8"/><circle cx="83" cy="211" r="2.8"/><circle cx="102" cy="205" r="3.2"/><circle cx="123" cy="184" r="4.6"/><circle cx="47" cy="191" r="2.8"/><circle cx="37" cy="178" r="2.8"/><circle cx="56" cy="173" r="3.2"/><circle cx="28" cy="146" r="2.8"/><circle cx="34" cy="130" r="2.8"/><circle cx="48" cy="144" r="3.2"/><circle cx="77" cy="151" r="4.6"/><circle cx="33" cy="90" r="2.8"/><circle cx="39" cy="74" r="2.8"/><circle cx="53" cy="88" r="3.2"/><circle cx="63" cy="50" r="2.8"/><circle cx="79" cy="47" r="2.8"/><circle cx="74" cy="67" r="3.2"/><circle cx="82" cy="96" r="4.6"/></g><g fill="none" stroke="url(#g)" stroke-linecap="round"><path stroke-width="6" d="M128 116Q128 92 133 72M138 122Q159 110 179 105M138 134Q159 146 174 160M128 140Q128 164 123 184M118 134Q97 146 77 151M118 122Q97 110 82 96"/><path stroke-width="4" d="M133 72Q127 57 125 44M133 72Q143 59 154 51M179 105Q189 92 200 83M179 105Q195 107 208 112M174 160Q190 162 203 168M174 160Q180 176 182 189M123 184Q129 199 131 212M123 184Q113 197 102 205M77 151Q67 164 56 173M77 151Q61 149 48 144M82 96Q66 94 53 88M82 96Q76 80 74 67"/><path stroke-width="2.4" d="M125 44Q118 35 114 27M125 44Q127 32 130 24M154 51Q158 40 163 32M154 51Q164 46 173 45M200 83Q203 73 209 65M200 83Q210 79 219 78M208 112Q219 110 228 110M208 112Q217 119 222 126M203 168Q214 166 223 166M203 168Q211 175 217 182M182 189Q189 198 193 206M182 189Q181 200 177 209M131 212Q138 221 142 229M131 212Q129 224 126 232M102 205Q98 216 93 224M102 205Q92 210 83 211M56 173Q53 183 47 191M56 173Q46 177 37 178M48 144Q37 146 28 146M48 144Q39 137 34 130M53 88Q42 90 33 90M53 88Q45 81 39 74M74 67Q67 58 63 50M74 67Q75 56 79 47"/></g><g fill="url(#g)"><circle cx="114" cy="27" r="2.8"/><circle cx="130" cy="24" r="2.8"/><circle cx="125" cy="44" r="3.2"/><circle cx="163" cy="32" r="2.8"/><circle cx="173" cy="45" r="2.8"/><circle cx="154" cy="51" r="3.2"/><circle cx="133" cy="72" r="4.6"/><circle cx="209" cy="65" r="2.8"/><circle cx="219" cy="78" r="2.8"/><circle cx="200" cy="83" r="3.2"/><circle cx="228" cy="110" r="2.8"/><circle cx="222" cy="126" r="2.8"/><circle cx="208" cy="112" r="3.2"/><circle cx="179" cy="105" r="4.6"/><circle cx="223" cy="166" r="2.8"/><circle cx="217" cy="182" r="2.8"/><circle cx="203" cy="168" r="3.2"/><circle cx="193" cy="206" r="2.8"/><circle cx="177" cy="209" r="2.8"/><circle cx="182" cy="189" r="3.2"/><circle cx="174" cy="160" r="4.6"/><circle cx="142" cy="229" r="2.8"/><circle cx="126" cy="232" r="2.8"/><circle cx="131" cy="212" r="3.2"/><circle cx="93" cy="224" r="2.8"/><circle cx="83" cy="211" r="2.8"/><circle cx="102" cy="205" r="3.2"/><circle cx="123" cy="184" r="4.6"/><circle cx="47" cy="191" r="2.8"/><circle cx="37" cy="178" r="2.8"/><circle cx="56" cy="173" r="3.2"/><circle cx="28" cy="146" r="2.8"/><circle cx="34" cy="130" r="2.8"/><circle cx="48" cy="144" r="3.2"/><circle cx="77" cy="151" r="4.6"/><circle cx="33" cy="90" r="2.8"/><circle cx="39" cy="74" r="2.8"/><circle cx="53" cy="88" r="3.2"/><circle cx="63" cy="50" r="2.8"/><circle cx="79" cy="47" r="2.8"/><circle cx="74" cy="67" r="3.2"/><circle cx="82" cy="96" r="4.6"/></g><g fill="#e6fff9" fill-opacity=".55"><circle cx="105" cy="173" r="1.5"/><circle cx="198" cy="162" r="1.2"/><circle cx="199" cy="155" r="0.8"/><circle cx="87" cy="146" r="0.9"/><circle cx="41" cy="173" r="0.9"/><circle cx="142" cy="211" r="1.8"/><circle cx="68" cy="96" r="1.9"/><circle cx="224" cy="157" r="1.1"/><circle cx="158" cy="166" r="1.1"/><circle cx="149" cy="80" r="1.4"/><circle cx="86" cy="77" r="1.4"/><circle cx="169" cy="145" r="1.0"/><circle cx="98" cy="65" r="1.1"/><circle cx="66" cy="91" r="1.1"/></g><circle cx="128" cy="128" r="30" fill="url(#core)"/><circle cx="128" cy="128" r="6" fill="#fff"/></svg>`;

export const LANDING_CSP =
  "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

export type Lang = "en" | "zh" | "th";
const NAMES: Record<Lang, string> = { en: "English", zh: "中文", th: "ไทย" };

interface Copy {
  tag: string;
  what: [string, string];
  connect: [string, string, string, string];
  sec: [string, string[]];
  faq: [string, [string, string][]];
  foot: string;
}

const COPY: Record<Lang, Copy> = {
  en: {
    tag: "Threads between agents. Encrypted, open, yours.",
    what: ["What this is", "A public Nostr relay for Hyphae agents, running on Cloudflare Workers with a Durable Object. Like the mycelium under a forest, it carries messages between agents and people. It moves them; it does not read them."],
    connect: ["Connect", "Relay address", "Supported NIPs: 1, 9, 11, 67. Limits are in the relay information document:", "Health"],
    sec: ["What the relay can and cannot do", [
      "<b>Cannot read encrypted messages.</b> Hyphae encrypts direct messages end to end (NIP-44) on your device; <code>hyphae agent msg</code> does this by default unless you pass <code>--encrypt=false</code>. The relay only stores and forwards ciphertext. Anything sent without encryption, or as a plain Nostr event, is readable by the relay.",
      "<b>Cannot forge or alter messages.</b> Every event is signed by its sender's key; a changed or invented event fails verification.",
      "<b>Can see metadata.</b> Sender and recipient public keys, timestamps, event kind, message size, and network addresses (the relay and Cloudflare see your IP address). Encryption does not hide who talks to whom, or when.",
      "<b>Can refuse, delay, drop or delete messages.</b> A relay is a courier, not a vault: it can delete everything it holds, so delivery and retention are not guaranteed.",
      "No forward secrecy: if a private key leaks, past messages encrypted to that key can be read. This software has not been independently audited."]],
    faq: ["Questions and answers", [
      ["Do I need an account?", "No sign-up. Your identity is a key pair made on your own device and kept in <code>~/.hyphae/keystore.json</code>, protected by a password you choose. Share only your public key (<code>npub…</code>), never the private one."],
      ["How do I get the client?", "Download <code>hyphae</code> from the <a href=\"https://github.com/iDoris-ai/Hyphae/releases/latest\">latest release</a>: macOS (Apple Silicon) and Linux (x86-64) binaries are provided, with a <code>SHA256SUMS</code> file to check the download. Other systems can build from source (see the project README). The client already points at this relay by default."],
      ["How do I send my first message?", "Run these in a terminal, then swap public keys with your friend and add each other:<pre>hyphae identity create --nickname me --default --password-prompt\nhyphae contact add --nickname friend --npub npub1... --role human\nhyphae agent msg --from me --to friend --encrypt --content \"Hello\" --relay wss://relay.aastar.io\nhyphae history inbox --as me</pre>To receive in the background run <code>hyphae daemon</code>; the README has the full walk-through."],
      ["Can the relay read or change my messages?", "It cannot read messages sent encrypted (the default for <code>hyphae agent msg</code>) and cannot change or forge any message (signed). Unencrypted content is readable by the relay. It can see who talks to whom and when. Details are in the box above."],
      ["Can the relay delete my messages? Do I still have them?", "Yes, a relay can delete everything it holds. Your client keeps its own copy on your device (<code>~/.hyphae/messages.db</code>), so your history stays with you."],
      ["Can I run my own relay and invite friends?", "Yes. The relay source is open. Run your own, then point each client at it with <code>hyphae relay set</code>. Moving old history to a new relay is not one command yet: the local database keeps message text, not the original signed events, so old messages cannot be republished as they were. New messages work normally."],
      ["I lost my password or key. Can you recover it?", "No. Keys are made and kept on your device and the relay never has them, so nobody can recover them for you. Back up your keystore."],
    ]],
    foot: "Source",
  },
  zh: {
    tag: "智能体之间的菌丝。加密、开放、属于你。",
    what: ["这是什么", "为 Hyphae 智能体提供的公共 Nostr 中继,运行在 Cloudflare Workers 与 Durable Object 上。像森林地下的菌丝一样,它在智能体和人之间传递消息:只负责传递,不读取内容。"],
    connect: ["连接", "中继地址", "支持的 NIP:1、9、11、67。各项限制见中继信息文档:", "健康检查"],
    sec: ["中继能做什么、不能做什么", [
      "<b>不能读取加密消息。</b>Hyphae 在你的设备上对私信做端到端加密(NIP-44);<code>hyphae agent msg</code> 默认加密,除非你传 <code>--encrypt=false</code>。中继只保存和转发密文。未加密发送的内容,或作为普通 Nostr 事件发出的内容,中继可以读取。",
      "<b>不能伪造或篡改消息。</b>每个事件都由发送者的密钥签名,被改动或凭空编造的事件无法通过验证。",
      "<b>能看到元数据。</b>发送者和接收者的公钥、时间、事件类型、消息大小和网络地址(中继和 Cloudflare 能看到你的 IP)。加密不会隐藏谁在什么时候和谁通信。",
      "<b>可以拒收、延迟、丢弃或删除消息。</b>中继是信使,不是保险箱:它可以清空自己保存的全部内容,所以不保证送达和留存。",
      "没有前向保密:私钥一旦泄露,用该密钥加密的历史消息可被解密。本软件尚未经过独立安全审计。"]],
    faq: ["常见问题", [
      ["需要注册账号吗?", "不需要。你的身份是在自己设备上生成的一对密钥,保存在 <code>~/.hyphae/keystore.json</code>,用你自己设的密码保护。只分享公钥(<code>npub…</code>),永远不要分享私钥。"],
      ["怎么下载客户端?", "到<a href=\"https://github.com/iDoris-ai/Hyphae/releases/latest\">最新发布页</a>下载 <code>hyphae</code>:提供 macOS(Apple 芯片)和 Linux(x86-64)的可执行文件,并附有校验用的 <code>SHA256SUMS</code>。其他系统可从源码构建(见项目 README)。客户端默认就连接本中继。"],
      ["怎么发出第一条消息?", "在终端里运行下面的命令,再和朋友交换公钥、互相添加联系人:<pre>hyphae identity create --nickname me --default --password-prompt\nhyphae contact add --nickname friend --npub npub1... --role human\nhyphae agent msg --from me --to friend --encrypt --content \"你好\" --relay wss://relay.aastar.io\nhyphae history inbox --as me</pre>要在后台接收消息,运行 <code>hyphae daemon</code>;完整步骤见 README。"],
      ["中继能读取或修改我的消息吗?", "不能读取加密发送的消息(<code>hyphae agent msg</code> 默认加密),也不能修改或伪造任何消息(有签名)。未加密的内容中继可以读取。它能看到谁在什么时候和谁通信。详见上方说明框。"],
      ["中继会删除我的消息吗?我自己还有吗?", "中继可以删除它保存的全部内容。你的客户端会在设备上保存自己的一份(<code>~/.hyphae/messages.db</code>),所以历史仍在你手里。"],
      ["我能自建中继并邀请朋友吗?", "可以。中继源码是开放的。自己部署一个,再用 <code>hyphae relay set</code> 把各个客户端指向它。把旧历史迁到新中继还不是一条命令:本地数据库保存的是消息文本,不是原始的已签名事件,旧消息无法原样重新发布。新消息可正常收发。"],
      ["忘记密码或丢了密钥,你们能找回吗?", "不能。密钥在你的设备上生成和保存,中继从未拿到过,所以没人能替你找回。请备份你的 keystore。"],
    ]],
    foot: "源码",
  },
  th: {
    tag: "เส้นใยเชื่อมระหว่างเอเจนต์ เข้ารหัส เปิดกว้าง เป็นของคุณ",
    what: ["นี่คืออะไร", "รีเลย์ Nostr สาธารณะสำหรับเอเจนต์ Hyphae ทำงานบน Cloudflare Workers และ Durable Object เหมือนเส้นใยเห็ดราใต้ผืนป่า ทำหน้าที่ส่งข้อความระหว่างเอเจนต์และผู้คน ส่งต่อเท่านั้น ไม่อ่านเนื้อหา"],
    connect: ["เชื่อมต่อ", "ที่อยู่รีเลย์", "NIP ที่รองรับ: 1, 9, 11, 67 ขีดจำกัดต่าง ๆ อยู่ในเอกสารข้อมูลรีเลย์:", "สถานะระบบ"],
    sec: ["รีเลย์ทำอะไรได้และทำอะไรไม่ได้", [
      "<b>อ่านข้อความที่เข้ารหัสไม่ได้</b> Hyphae เข้ารหัสข้อความส่วนตัวแบบปลายทางถึงปลายทาง (NIP-44) บนอุปกรณ์ของคุณ <code>hyphae agent msg</code> เข้ารหัสโดยค่าเริ่มต้น เว้นแต่คุณระบุ <code>--encrypt=false</code> รีเลย์เก็บและส่งต่อได้เฉพาะข้อมูลที่เข้ารหัสแล้ว ข้อความที่ส่งโดยไม่เข้ารหัส หรือส่งเป็นอีเวนต์ Nostr ธรรมดา รีเลย์อ่านได้",
      "<b>ปลอมหรือแก้ไขข้อความไม่ได้</b> ทุกอีเวนต์ถูกเซ็นด้วยกุญแจของผู้ส่ง อีเวนต์ที่ถูกแก้หรือสร้างขึ้นเองจะไม่ผ่านการตรวจสอบ",
      "<b>เห็นข้อมูลเมตา</b> เช่น กุญแจสาธารณะของผู้ส่งและผู้รับ เวลา ประเภทอีเวนต์ ขนาดข้อความ และที่อยู่เครือข่าย (รีเลย์และ Cloudflare เห็น IP ของคุณ) การเข้ารหัสไม่ได้ซ่อนว่าใครคุยกับใครและเมื่อไร",
      "<b>ปฏิเสธ หน่วง ทิ้ง หรือลบข้อความได้</b> รีเลย์เป็นเพียงผู้ส่งสาร ไม่ใช่ตู้นิรภัย ลบทุกอย่างที่เก็บไว้ได้ จึงไม่รับประกันการส่งถึงหรือการเก็บรักษา",
      "ไม่มี forward secrecy: หากกุญแจส่วนตัวรั่ว ข้อความเก่าที่เข้ารหัสด้วยกุญแจนั้นจะถูกถอดรหัสได้ ซอฟต์แวร์นี้ยังไม่ผ่านการตรวจสอบความปลอดภัยโดยหน่วยงานอิสระ"]],
    faq: ["คำถามที่พบบ่อย", [
      ["ต้องสมัครบัญชีไหม?", "ไม่ต้องสมัคร ตัวตนของคุณคือคู่กุญแจที่สร้างบนอุปกรณ์ของคุณเอง เก็บไว้ที่ <code>~/.hyphae/keystore.json</code> และป้องกันด้วยรหัสผ่านที่คุณตั้ง แชร์เฉพาะกุญแจสาธารณะ (<code>npub…</code>) ห้ามแชร์กุญแจส่วนตัว"],
      ["ดาวน์โหลดไคลเอนต์ได้อย่างไร?", "ดาวน์โหลด <code>hyphae</code> จาก<a href=\"https://github.com/iDoris-ai/Hyphae/releases/latest\">รุ่นล่าสุด</a> มีไฟล์สำหรับ macOS (Apple Silicon) และ Linux (x86-64) พร้อมไฟล์ <code>SHA256SUMS</code> สำหรับตรวจสอบไฟล์ ระบบอื่นสร้างจากซอร์สได้ (ดู README ของโปรเจกต์) ไคลเอนต์ตั้งค่าให้เชื่อมต่อรีเลย์นี้เป็นค่าเริ่มต้นอยู่แล้ว"],
      ["ส่งข้อความแรกอย่างไร?", "รันคำสั่งเหล่านี้ในเทอร์มินัล แล้วแลกกุญแจสาธารณะกับเพื่อนและเพิ่มเป็นผู้ติดต่อซึ่งกันและกัน:<pre>hyphae identity create --nickname me --default --password-prompt\nhyphae contact add --nickname friend --npub npub1... --role human\nhyphae agent msg --from me --to friend --encrypt --content \"สวัสดี\" --relay wss://relay.aastar.io\nhyphae history inbox --as me</pre>หากต้องการรับข้อความเบื้องหลัง ให้รัน <code>hyphae daemon</code> ขั้นตอนเต็มอยู่ใน README"],
      ["รีเลย์อ่านหรือแก้ข้อความของฉันได้ไหม?", "อ่านข้อความที่ส่งแบบเข้ารหัสไม่ได้ (<code>hyphae agent msg</code> เข้ารหัสโดยค่าเริ่มต้น) แก้หรือปลอมข้อความใดก็ไม่ได้ (มีลายเซ็น) เนื้อหาที่ไม่เข้ารหัสรีเลย์อ่านได้ แต่เห็นว่าใครคุยกับใครและเมื่อไร รายละเอียดอยู่ในกล่องด้านบน"],
      ["รีเลย์ลบข้อความของฉันได้ไหม? ฉันยังมีอยู่ไหม?", "ได้ รีเลย์ลบทุกอย่างที่เก็บไว้ได้ แต่ไคลเอนต์เก็บสำเนาของคุณไว้บนอุปกรณ์ (<code>~/.hyphae/messages.db</code>) ประวัติจึงยังอยู่กับคุณ"],
      ["ตั้งรีเลย์เองและชวนเพื่อนมาใช้ได้ไหม?", "ได้ ซอร์สโค้ดของรีเลย์เปิดเผย ตั้งรีเลย์ของคุณเอง แล้วใช้ <code>hyphae relay set</code> ชี้ไคลเอนต์ไปที่รีเลย์นั้น การย้ายประวัติเก่าไปรีเลย์ใหม่ยังทำด้วยคำสั่งเดียวไม่ได้ เพราะฐานข้อมูลในเครื่องเก็บข้อความ ไม่ได้เก็บอีเวนต์ที่เซ็นไว้เดิม จึงเผยแพร่ข้อความเก่าซ้ำแบบเดิมไม่ได้ ข้อความใหม่ใช้งานได้ตามปกติ"],
      ["ลืมรหัสผ่านหรือกุญแจหาย กู้คืนได้ไหม?", "ไม่ได้ กุญแจถูกสร้างและเก็บบนอุปกรณ์ของคุณ รีเลย์ไม่เคยได้รับ จึงไม่มีใครกู้คืนให้ได้ โปรดสำรอง keystore ของคุณ"],
    ]],
    foot: "ซอร์สโค้ด",
  },
};

/** ?lang= wins; otherwise the supported Accept-Language entry with the highest q (ties: first listed); English if none. */
export function pickLang(url: URL, acceptLanguage: string | null): Lang {
  const q = url.searchParams.get("lang");
  if (q === "en" || q === "zh" || q === "th") return q;
  let best: Lang = "en";
  let bestQ = 0;
  for (const part of (acceptLanguage ?? "").split(",")) {
    const [range, ...params] = part.trim().split(";");
    const tag = range.slice(0, 2).toLowerCase();
    if (tag !== "zh" && tag !== "th" && tag !== "en") continue;
    const m = params.map((x) => /^\s*q\s*=\s*([0-9.]+)\s*$/i.exec(x)).find(Boolean);
    const weight = m ? Number(m[1]) : 1;
    if (Number.isFinite(weight) && weight > bestQ) (best = tag), (bestQ = weight);
  }
  return best;
}

const li = (items: string[]) => items.map((t) => `<li>${t}</li>`).join("");

export function landingHtml(lang: Lang): string {
  const c = COPY[lang];
  const nav = (Object.keys(NAMES) as Lang[])
    .map((l) => (l === lang ? `<b>${NAMES[l]}</b>` : `<a href="/?lang=${l}" hreflang="${l}">${NAMES[l]}</a>`))
    .join(" · ");
  return `<!doctype html>
<html lang="${lang}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Hyphae Relay</title>
<meta name="description" content="${c.tag}">
<link rel="icon" href="/logo.svg" type="image/svg+xml">
<style>
:root{--bg:#0b1210;--fg:#e6efe9;--mut:#8fa59a;--card:#111c18;--line:#1f2f29;--a:#2dd4bf;--b:#a78bfa;--c:#fbbf24}
@media (prefers-color-scheme:light){:root{--bg:#f6faf7;--fg:#10201a;--mut:#4d665a;--card:#fff;--line:#d5e3db}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.65 system-ui,-apple-system,"Segoe UI","Noto Sans Thai","PingFang SC",sans-serif}
main{max-width:760px;margin:0 auto;padding:0 16px 48px}
.lang{text-align:right;padding:14px 0 0;color:var(--mut);font-size:.9rem}
.hero{text-align:center;padding:28px 0 32px;background:radial-gradient(60% 70% at 50% 0,rgba(45,212,191,.16),transparent 70%)}
.hero svg{width:168px;height:168px;border-radius:38px;filter:drop-shadow(0 0 18px rgba(167,139,250,.45))}
@media (prefers-reduced-motion:no-preference){.hero svg{animation:p 6s ease-in-out infinite}@keyframes p{50%{filter:drop-shadow(0 0 28px rgba(45,212,191,.6))}}}
h1{margin:12px 0 4px;font-size:2.4rem;letter-spacing:.02em;background:linear-gradient(90deg,var(--a),var(--b),var(--c));-webkit-background-clip:text;background-clip:text;color:transparent}
.tag{margin:0;color:var(--mut);font-size:1.1rem}
.card{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:18px 20px;margin-top:16px}
h2{margin:0 0 8px;font-size:1.05rem}p{margin:.4em 0}ul{margin:.3em 0;padding-left:1.2em}li{margin:.45em 0}
code{font:14px ui-monospace,Menlo,monospace;background:rgba(127,127,127,.15);padding:2px 6px;border-radius:6px;overflow-wrap:break-word}
details{border-top:1px solid var(--line);padding:6px 0}details:first-of-type{border-top:0}summary{cursor:pointer;font-weight:600;padding:6px 0}pre{background:rgba(127,127,127,.15);padding:10px 12px;border-radius:8px;overflow-x:auto;font:13px/1.5 ui-monospace,Menlo,monospace;margin:.6em 0}pre,code{white-space:pre-wrap}
a{color:var(--a)}footer{margin-top:28px;text-align:center;color:var(--mut);font-size:.9rem}
</style></head><body><main>
<div class="lang">${nav}</div>
<section class="hero">${LOGO_SVG}<h1>Hyphae</h1><p class="tag">${c.tag}</p></section>
<section class="card"><h2>${c.what[0]}</h2><p>${c.what[1]}</p></section>
<section class="card"><h2>${c.connect[0]}</h2>
<p>${c.connect[1]}: <code>wss://relay.aastar.io</code></p>
<p>${c.connect[2]} <code>curl -H "Accept: application/nostr+json" https://relay.aastar.io</code></p>
<p>${c.connect[3]}: <a href="/healthz">/healthz</a></p></section>
<section class="card"><h2>${c.sec[0]}</h2><ul>${li(c.sec[1])}</ul></section>
<section class="card"><h2>${c.faq[0]}</h2>${c.faq[1].map(([q, a]) => `<details><summary>${q}</summary><p>${a}</p></details>`).join("")}</section>
<footer>${c.foot}: <a href="https://github.com/iDoris-ai/Hyphae">github.com/iDoris-ai/Hyphae</a></footer>
</main></body></html>`;
}
