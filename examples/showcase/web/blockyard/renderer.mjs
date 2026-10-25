import { palette } from "./mesh.mjs";
const vertex = `#version 300 es
precision highp float;
layout(location=0) in vec3 position;
layout(location=1) in vec3 color;
layout(location=2) in float alpha;
layout(location=3) in float emission;
layout(location=4) in vec4 instancePosition;
layout(location=5) in vec3 instanceColor;
uniform mat4 viewProjection;
uniform bool instanced;
out vec3 worldPosition;
out vec3 surfaceColor;
out float surfaceAlpha;
out float surfaceEmission;
void main(){
 vec3 p=position;
 if(instanced){float c=cos(instancePosition.w),s=sin(instancePosition.w);p.xz=mat2(c,-s,s,c)*p.xz;p+=instancePosition.xyz;}
 worldPosition=p;surfaceColor=color*(instanced?instanceColor:vec3(1));surfaceAlpha=alpha;surfaceEmission=emission;
 gl_Position=viewProjection*vec4(p,1);
}`;
const fragment = `#version 300 es
precision highp float;
in vec3 worldPosition;
in vec3 surfaceColor;
in float surfaceAlpha;
in float surfaceEmission;
uniform vec3 eye;
uniform vec3 sky;
uniform float daylight;
uniform float fogDistance;
out vec4 result;
void main(){
 vec3 cell=floor(worldPosition+vec3(.002));
 float grain=fract(sin(dot(cell,vec3(12.9898,78.233,45.164)))*43758.5453);
 vec3 local=fract(worldPosition+vec3(.0001));
 vec3 lines=min(local,1.-local);
 float edge=min(max(lines.x,lines.y),min(max(lines.y,lines.z),max(lines.x,lines.z)));
 float detail=mix(.89,1.,smoothstep(0.,.025,edge))*(.96+grain*.08);
 vec3 lit=surfaceColor*detail*mix(daylight,1.3,surfaceEmission);
 float fog=smoothstep(fogDistance*.48,fogDistance,length(worldPosition-eye));
 result=vec4(mix(lit,sky,fog),surfaceAlpha);
}`;
function program(gl) {
  const p = gl.createProgram();
  for (const [type, source] of [
    [gl.VERTEX_SHADER, vertex],
    [gl.FRAGMENT_SHADER, fragment],
  ]) {
    const s = gl.createShader(type);
    gl.shaderSource(s, source);
    gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS))
      throw Error(gl.getShaderInfoLog(s));
    gl.attachShader(p, s);
    gl.deleteShader(s);
  }
  gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS))
    throw Error(gl.getProgramInfoLog(p));
  return p;
}
function perspective(out, fov, aspect, near, far) {
  out.fill(0);
  const f = 1 / Math.tan(fov / 2);
  out[0] = f / aspect;
  out[5] = f;
  out[10] = (far + near) / (near - far);
  out[11] = -1;
  out[14] = (2 * far * near) / (near - far);
}
function matrix(eye, yaw, pitch, aspect) {
  const sy = Math.sin(yaw),
    cy = Math.cos(yaw),
    sp = Math.sin(pitch),
    cp = Math.cos(pitch),
    r = [cy, 0, -sy],
    u = [sy * sp, cp, cy * sp],
    f = [-sy * cp, sp, -cy * cp];
  const v = new Float32Array([
    r[0],
    u[0],
    -f[0],
    0,
    r[1],
    u[1],
    -f[1],
    0,
    r[2],
    u[2],
    -f[2],
    0,
    -r.reduce((s, x, i) => s + x * eye[i], 0),
    -u.reduce((s, x, i) => s + x * eye[i], 0),
    f.reduce((s, x, i) => s + x * eye[i], 0),
    1,
  ]);
  const p = new Float32Array(16),
    out = new Float32Array(16);
  perspective(p, (75 * Math.PI) / 180, aspect, 0.05, 150);
  for (let c = 0; c < 4; c++)
    for (let r = 0; r < 4; r++)
      for (let k = 0; k < 4; k++) out[c * 4 + r] += p[k * 4 + r] * v[c * 4 + k];
  return out;
}
function box(vertices, min, max, color, emission = 0) {
  for (let axis = 0; axis < 3; axis++)
    for (const sign of [-1, 1]) {
      const u = (axis + 1) % 3,
        v = (axis + 2) % 3,
        shade = axis === 1 ? (sign > 0 ? 1 : 0.5) : axis === 0 ? 0.85 : 0.7;
      const points = [
        [min[u], min[v]],
        [max[u], min[v]],
        [max[u], max[v]],
        [min[u], max[v]],
      ];
      for (const i of sign > 0 ? [0, 1, 2, 0, 2, 3] : [0, 2, 1, 0, 3, 2]) {
        const p = [0, 0, 0];
        p[axis] = sign > 0 ? max[axis] : min[axis];
        p[u] = points[i][0];
        p[v] = points[i][1];
        vertices.push(...p, ...color.map((c) => c * shade), 1, emission);
      }
    }
}
export class Renderer {
  constructor(canvas) {
    this.canvas = canvas;
    this.gl = canvas.getContext("webgl2", {
      antialias: false,
      alpha: false,
      powerPreference: "high-performance",
    });
    if (!this.gl) throw Error("This browser needs WebGL 2 for the 3D world.");
    const gl = this.gl;
    this.program = program(gl);
    this.meshes = new Map();
    this.scale = 1;
    this.distance = 44;
    this.triangles = 0;
    this.uniforms = Object.fromEntries(
      [
        "viewProjection",
        "instanced",
        "eye",
        "sky",
        "daylight",
        "fogDistance",
      ].map((name) => [name, gl.getUniformLocation(this.program, name)]),
    );
    gl.enable(gl.DEPTH_TEST);
    gl.enable(gl.CULL_FACE);
    gl.cullFace(gl.BACK);
    gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
    const avatar = [];
    box(avatar, [-0.22, 0.55, -0.14], [0.22, 1.18, 0.14], [1, 1, 1]);
    box(avatar, [-0.24, 1.18, -0.24], [0.24, 1.66, 0.24], [1.14, 1.02, 0.85]);
    box(avatar, [-0.2, 0, -0.13], [-0.025, 0.55, 0.13], [0.45, 0.48, 0.52]);
    box(avatar, [0.025, 0, -0.13], [0.2, 0.55, 0.13], [0.45, 0.48, 0.52]);
    box(avatar, [-0.16, 1.4, -0.25], [-0.09, 1.47, -0.24], [0.08, 0.09, 0.1]);
    box(avatar, [0.09, 1.4, -0.25], [0.16, 1.47, -0.24], [0.08, 0.09, 0.1]);
    this.avatar = this.upload(new Float32Array(avatar));
    this.instances = gl.createBuffer();
    gl.bindVertexArray(this.avatar.vao);
    gl.bindBuffer(gl.ARRAY_BUFFER, this.instances);
    gl.enableVertexAttribArray(4);
    gl.vertexAttribPointer(4, 4, gl.FLOAT, false, 28, 0);
    gl.vertexAttribDivisor(4, 1);
    gl.enableVertexAttribArray(5);
    gl.vertexAttribPointer(5, 3, gl.FLOAT, false, 28, 16);
    gl.vertexAttribDivisor(5, 1);
    this.lines = this.upload(new Float32Array(0));
    this.instanceData = new Float32Array(512 * 7);
    new ResizeObserver(() => this.resize()).observe(canvas);
    this.resize();
    canvas.addEventListener("webglcontextlost", (e) => {
      e.preventDefault();
      canvas.dispatchEvent(
        new CustomEvent("rendererror", {
          detail: "The graphics context was lost. Reload to resume.",
        }),
      );
    });
  }
  resize() {
    const r = this.canvas.getBoundingClientRect(),
      ratio = Math.min(devicePixelRatio || 1, 1.5) * this.scale;
    const width = Math.max(1, Math.round(r.width * ratio)),
      height = Math.max(1, Math.round(r.height * ratio));
    if (this.canvas.width !== width || this.canvas.height !== height) {
      this.canvas.width = width;
      this.canvas.height = height;
      this.gl.viewport(0, 0, width, height);
    }
  }
  upload(data) {
    const gl = this.gl,
      vao = gl.createVertexArray(),
      buffer = gl.createBuffer();
    gl.bindVertexArray(vao);
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(gl.ARRAY_BUFFER, data, gl.STATIC_DRAW);
    for (const [at, size, offset] of [
      [0, 3, 0],
      [1, 3, 12],
      [2, 1, 24],
      [3, 1, 28],
    ]) {
      gl.enableVertexAttribArray(at);
      gl.vertexAttribPointer(at, size, gl.FLOAT, false, 32, offset);
    }
    return { vao, buffer, count: data.length / 8 };
  }
  dispose(mesh) {
    this.gl.deleteVertexArray(mesh.vao);
    this.gl.deleteBuffer(mesh.buffer);
  }
  set(key, x, z, opaque, alpha) {
    this.remove(key);
    this.meshes.set(key, {
      x,
      z,
      opaque: this.upload(opaque),
      alpha: this.upload(alpha),
    });
  }
  remove(key) {
    const old = this.meshes.get(key);
    if (old) {
      this.dispose(old.opaque);
      this.dispose(old.alpha);
      this.meshes.delete(key);
    }
  }
  clear() {
    for (const key of this.meshes.keys()) this.remove(key);
  }
  draw(camera, yaw, pitch, players, target, time = "day") {
    const gl = this.gl,
      u = this.uniforms,
      eye = [camera.x, camera.y + 1.55, camera.z];
    this.resize();
    const sky =
        time === "night"
          ? [0.07, 0.12, 0.19]
          : time === "sunset"
            ? [0.85, 0.65, 0.52]
            : [0.65, 0.8, 0.83],
      daylight = time === "night" ? 0.38 : time === "sunset" ? 0.85 : 1.05;
    gl.clearColor(...sky, 1);
    gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT);
    gl.useProgram(this.program);
    gl.uniformMatrix4fv(
      u.viewProjection,
      false,
      matrix(eye, yaw, pitch, this.canvas.width / this.canvas.height),
    );
    gl.uniform3fv(u.eye, eye);
    gl.uniform3fv(u.sky, sky);
    gl.uniform1f(u.daylight, daylight);
    gl.uniform1f(u.fogDistance, this.distance);
    gl.uniform1i(u.instanced, 0);
    const meshes = [...this.meshes.values()]
      .map((mesh) => ({
        mesh,
        distance:
          (mesh.x * 16 + 8 - eye[0]) ** 2 + (mesh.z * 16 + 8 - eye[2]) ** 2,
      }))
      .filter((entry) => entry.distance < (this.distance + 24) ** 2)
      .sort((a, b) => a.distance - b.distance);
    this.triangles = 0;
    for (const { mesh } of meshes) {
      gl.bindVertexArray(mesh.opaque.vao);
      gl.drawArrays(gl.TRIANGLES, 0, mesh.opaque.count);
      this.triangles += mesh.opaque.count / 3;
    }
    let count = 0;
    for (const p of players) {
      const at = count * 7,
        hue = (p.id * 0.61803398875) % 1,
        color = [
          0.65 + 0.25 * Math.sin(hue * 6.28),
          0.68 + 0.2 * Math.sin(hue * 6.28 + 2),
          0.72 + 0.2 * Math.sin(hue * 6.28 + 4),
        ];
      this.instanceData.set([p.x, p.y, p.z, p.yaw, ...color], at);
      count++;
    }
    if (count) {
      gl.uniform1i(u.instanced, 1);
      gl.bindVertexArray(this.avatar.vao);
      gl.bindBuffer(gl.ARRAY_BUFFER, this.instances);
      gl.bufferData(
        gl.ARRAY_BUFFER,
        this.instanceData.subarray(0, count * 7),
        gl.DYNAMIC_DRAW,
      );
      gl.drawArraysInstanced(gl.TRIANGLES, 0, this.avatar.count, count);
      gl.uniform1i(u.instanced, 0);
    }
    gl.enable(gl.BLEND);
    gl.depthMask(false);
    for (let i = meshes.length - 1; i >= 0; i--) {
      const mesh = meshes[i].mesh;
      gl.bindVertexArray(mesh.alpha.vao);
      gl.drawArrays(gl.TRIANGLES, 0, mesh.alpha.count);
      this.triangles += mesh.alpha.count / 3;
    }
    gl.depthMask(true);
    gl.disable(gl.BLEND);
    if (target) {
      const data = [],
        corners = [];
      for (let i = 0; i < 8; i++)
        corners.push([
          target.x + (i & 1 ? 1.003 : -0.003),
          target.y + (i & 2 ? 1.003 : -0.003),
          target.z + (i & 4 ? 1.003 : -0.003),
        ]);
      for (let i = 0; i < 8; i++)
        for (const bit of [1, 2, 4])
          if (!(i & bit))
            for (const point of [corners[i], corners[i | bit]])
              data.push(...point, 1, 0.88, 0.44, 1, 0.4);
      gl.bindVertexArray(this.lines.vao);
      gl.bindBuffer(gl.ARRAY_BUFFER, this.lines.buffer);
      gl.bufferData(gl.ARRAY_BUFFER, new Float32Array(data), gl.DYNAMIC_DRAW);
      gl.drawArrays(gl.LINES, 0, data.length / 8);
    }
  }
}
