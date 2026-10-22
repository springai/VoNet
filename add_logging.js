const fs = require('fs');
let content = fs.readFileSync('internal/vowifi/ims/rtp_media.go', 'utf8');

const old = '\t\t\tcase <-ticker.C:\r\n\t\t\t\t_ = media.WritePCM(silence)';
const nw = '\t\t\tcase <-ticker.C:\r\n\t\t\t\tif err := media.WritePCM(silence); err != nil {\r\n\t\t\t\t\tfmt.Printf("[RTP] silence send error: %v\\n", err)\r\n\t\t\t\t}';

if (!content.includes(old)) {
  console.log('ERROR: pattern not found');
  process.exit(1);
}
content = content.replace(old, nw);
fs.writeFileSync('internal/vowifi/ims/rtp_media.go', content);
console.log('OK: debug logging added');
