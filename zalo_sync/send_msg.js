import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';
import { Zalo, ThreadType } from 'zca-js';

const [,, groupId, message, tagAllFlag] = process.argv;

if (!groupId || !message) {
  console.error(JSON.stringify({ success: false, error: "Missing groupId or message" }));
  process.exit(1);
}

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const rootDir = path.resolve(__dirname, '..');
const sessionFile = path.join(rootDir, 'zalo_session.json');

if (!fs.existsSync(sessionFile)) {
  console.error(JSON.stringify({ success: false, error: "Chưa có file phiên zalo_session.json" }));
  process.exit(1);
}

const sessionRaw = fs.readFileSync(sessionFile, 'utf8');
const session = JSON.parse(sessionRaw);

const rawCookie = session.cookie;
if (!rawCookie) {
  console.error(JSON.stringify({ success: false, error: "Cookie trống trong zalo_session.json" }));
  process.exit(1);
}

const cookieArr = rawCookie.split(';').filter(Boolean).map(pair => {
  const idx = pair.indexOf('=');
  const key = pair.slice(0, idx).trim();
  const value = pair.slice(idx + 1).trim();
  return { key, value, domain: "chat.zalo.me" };
});

const zpdidMatch = rawCookie.match(/zpdid=([^;]+)/);
const imei = zpdidMatch ? zpdidMatch[1] : "41F-ar7nh3GT4fIRN_x6DnqUbfvTyi4m";

const zalo = new Zalo({ selfListen: false });
try {
  const api = await zalo.login({
    cookie: cookieArr,
    imei: imei,
    userAgent: session.ten_trinh_duyet || "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36"
  });

  const shouldTagAll = tagAllFlag === "true" || tagAllFlag === "1";
  
  let msgPayload = {
    msg: message
  };

  if (shouldTagAll) {
    const prefix = "@All ";
    msgPayload.msg = prefix + message;
    msgPayload.mentions = [
      {
        pos: 0,
        len: prefix.length,
        uid: "-1"
      }
    ];
  }

  const result = await api.sendMessage(msgPayload, groupId, ThreadType.Group);
  console.log(JSON.stringify({ success: true, result }));
} catch (err) {
  let errMsg = err.message || String(err);
  if (errMsg.includes("Tham số không hợp lệ")) {
    errMsg = `Tài khoản Bot hiện tại không ở trong nhóm này (ID: ${groupId}) hoặc đã bị kick/rời khỏi nhóm. Vui lòng thêm Bot vào nhóm trên Zalo trước khi gửi tin!`;
  }
  console.error(JSON.stringify({ success: false, error: errMsg }));
  process.exit(1);
}
