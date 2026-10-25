import { $, bytes, json } from "/assets/ui.mjs";

for (const button of document.querySelectorAll("[data-filter]")) {
  button.addEventListener("click", () => {
    let count = 0;
    for (const card of document.querySelectorAll(".example")) {
      card.hidden =
        button.dataset.filter !== "all" &&
        !card.dataset.categories.split(" ").includes(button.dataset.filter);
      if (!card.hidden) count++;
    }
    for (const tab of document.querySelectorAll("[data-filter]"))
      tab.setAttribute("aria-pressed", String(tab === button));
    $("filter-result").textContent = `${count} examples shown.`;
  });
}
async function load() {
  const tasks = [
    json("/blockyard/status").then(value=>{$("home-blockyard-count").textContent=`${value.players} / ${value.capacity} participants`;}),
    json("/gather/status").then((value) => {
      $("home-gather-count").textContent =
        `${value.players} / ${value.capacity}`;
    }),
    json("/api/meta").then((meta) => {
      $("binary-size").textContent = meta.binary_bytes
        ? bytes(meta.binary_bytes)
        : "Native";
      $("instance").textContent = meta.instance;
    }),
    json("/pulse/snapshot").then((snapshot) => {
      $("home-rss").textContent = bytes(snapshot.memory.rss);
    }),
    json("/mosaic/state?room=commons").then((board) => {
      const context = $("mosaic-preview").getContext("2d");
      board.pixels.forEach((pixel, i) => {
        context.fillStyle = `#${board.palette[pixel].toString(16).padStart(6, "0")}`;
        context.fillRect(i % 64, Math.floor(i / 64), 1, 1);
      });
    }),
    json("/fractal/api/config").then((config) => {
      document.querySelectorAll("[data-fractal]").forEach((image, i) => {
        image.src = `/fractal/tile/${config.render_version}/14/${7193 + (i % 2)}/${7651 + Math.floor(i / 2)}`;
      });
    }),
  ];
  await Promise.allSettled(tasks);
}
load();

const terrainCanvas=$("blockyard-preview");
if(terrainCanvas){
 const c=terrainCanvas.getContext("2d"), ox=330,oy=55,s=22;
 const polygon=(points,color)=>{c.fillStyle=color;c.beginPath();points.forEach(([x,y],i)=>i?c.lineTo(x,y):c.moveTo(x,y));c.closePath();c.fill();};
 function cube(x,y,z,colors){const px=ox+(x-z)*s,py=oy+(x+z)*s*.5-y*s;polygon([[px,py],[px+s,py+s*.5],[px,py+s],[px-s,py+s*.5]],colors[0]);polygon([[px-s,py+s*.5],[px,py+s],[px,py+s*2],[px-s,py+s*1.5]],colors[1]);polygon([[px,py+s],[px+s,py+s*.5],[px+s,py+s*1.5],[px,py+s*2]],colors[2]);}
 for(let z=0;z<8;z++)for(let x=0;x<8;x++){const h=x>4&&z<3?1:0;cube(x,h,z,["#87a464","#64774c","#526b43"]);if((x===2&&z===2)||(x===6&&z===5)){cube(x,h+1,z,["#a98a61","#786044","#66503b"]);cube(x,h+2,z,["#71964d","#527b43","#426b3b"]);cube(x,h+3,z,["#87aa5c","#638747","#4d763f"]);}if(x>=3&&x<=5&&z>=4&&z<=6&&!(x===4&&z===6)){cube(x,h+1,z,["#d5bd8d","#b29b73","#a38a66"]);if(z===4||x===3||x===5)cube(x,h+2,z,["#c6ad7a","#aa9068","#947e60"]);}}
}
