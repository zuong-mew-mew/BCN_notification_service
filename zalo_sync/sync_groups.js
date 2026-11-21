import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';
import { Zalo } from 'zca-js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const rootDir = path.resolve(__dirname, '..');
const sessionFile = path.join(rootDir, 'zalo_session.json');
const groupFile = path.join(rootDir, 'group_zalo.json');

if (!fs.existsSync(sessionFile)) {
  console.log("[ZCA] Chưa có file zalo_session.json");
  process.exit(0);
}

const sessionRaw = fs.readFileSync(sessionFile, 'utf8');
const session = JSON.parse(sessionRaw);

const rawCookie = session.cookie;
if (!rawCookie) {
  console.log("[ZCA] Cookie trống trong zalo_session.json");
  process.exit(0);
}

const cookieArr = rawCookie.split(';').filter(Boolean).map(pair => {
  const idx = pair.indexOf('=');
  const key = pair.slice(0, idx).trim();
  const value = pair.slice(idx + 1).trim();
  return { key, value, domain: "chat.zalo.me" };
});

const zpdidMatch = rawCookie.match(/zpdid=([^;]+)/);
const imei = zpdidMatch ? zpdidMatch[1] : "41F-ar7nh3GT4fIRN_x6DnqUbfvTyi4m";

console.log("[ZCA] Starting sync for IMEI:", imei);

const zalo = new Zalo({ selfListen: false });
try {
  const api = await zalo.login({
    cookie: cookieArr,
    imei: imei,
    userAgent: session.ten_trinh_duyet || "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36"
  });

  console.log("[ZCA] Logged in successfully!");

  const groupsRes = await api.getAllGroups();
  const gridMap = groupsRes.gridVerMap || (groupsRes.data && groupsRes.data.gridVerMap) || {};
  const groupIds = Object.keys(gridMap);
  console.log(`[ZCA] Total groups found from Zalo: ${groupIds.length}`);

  let allGroupDetails = [];

  for (let i = 0; i < groupIds.length; i += 50) {
    const chunk = groupIds.slice(i, i + 50);
    try {
      const infoRes = await api.getGroupInfo(chunk);
      const gridInfoMap = infoRes.gridInfoMap || (infoRes.data && infoRes.data.gridInfoMap) || {};
      for (const [gid, ginfo] of Object.entries(gridInfoMap)) {
        allGroupDetails.push({
          id: gid,
          name: ginfo.name || `Nhóm Zalo (${gid})`,
          is_test: gid === "5129297003586652280",
          is_private: true,
          source: "zalo_live",
          badge: gid === "5129297003586652280" ? "AN TOÀN" : "ZALO THẬT",
          mem_count: ginfo.totalMember || 0,
          admin_ids: ginfo.adminIds || [],
          creator_id: ginfo.creatorId || "",
          avt: ginfo.avt || ""
        });
      }
    } catch (err) {
      console.error(`[ZCA] Error fetching chunk ${i}:`, err.message);
    }
  }

  allGroupDetails.sort((a, b) => {
    if (a.is_test) return -1;
    if (b.is_test) return 1;
    return (b.mem_count || 0) - (a.mem_count || 0);
  });

  console.log(`[ZCA] Successfully resolved ${allGroupDetails.length} groups with names and member counts!`);
  fs.writeFileSync(groupFile, JSON.stringify(allGroupDetails, null, 2), 'utf8');
  console.log(`[ZCA] Saved to ${groupFile}`);

} catch (err) {
  console.error("[ZCA] Login/Fetch Error:", err);
}
