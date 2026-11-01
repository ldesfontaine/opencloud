// Un récepteur de webhook pour le banc : il écrit chaque appel reçu dans un fichier.
// Usage : node webhook-sink.mjs <port> <fichier>
import http from "node:http";
import fs from "node:fs";
const [port, file] = [process.argv[2], process.argv[3]];
http.createServer((req, res) => {
  let body = "";
  req.on("data", (chunk) => (body += chunk));
  req.on("end", () => {
    fs.appendFileSync(file, JSON.stringify({ at: new Date().toISOString(), url: req.url, headers: { "content-type": req.headers["content-type"] }, body: body.slice(0, 500) }) + "\n");
    res.writeHead(204).end();
  });
}).listen(Number(port), "127.0.0.1");
