// Vanilla JS Liquid Glass Effect
// Adapted from Shu Ding (https://github.com/shuding/liquid-glass)

(function () {
  'use strict';

  // Check if liquid glass already exists and destroy it
  if (window.liquidGlass) {
    if (typeof window.liquidGlass.destroy === 'function') {
      window.liquidGlass.destroy();
    }
  }

  // Utility functions
  function smoothStep(a, b, t) {
    t = Math.max(0, Math.min(1, (t - a) / (b - a)));
    return t * t * (3 - 2 * t);
  }

  function length(x, y) {
    return Math.sqrt(x * x + y * y);
  }

  function roundedRectSDF(x, y, width, height, radius) {
    const qx = Math.abs(x) - width + radius;
    const qy = Math.abs(y) - height + radius;
    return Math.min(Math.max(qx, qy), 0) + length(Math.max(qx, 0), Math.max(qy, 0)) - radius;
  }

  function texture(x, y) {
    return { type: 't', x, y };
  }

  function generateId() {
    return 'liquid-glass-' + Math.random().toString(36).substr(2, 9);
  }

  // ==========================================
  // 1. ORIGINAL SHUDING DRAGGABLE LENS SHADER
  // ==========================================
  class Shader {
    constructor(options = {}) {
      this.width = options.width || 100;
      this.height = options.height || 100;
      this.fragment = options.fragment || ((uv) => texture(uv.x, uv.y));
      this.canvasDPI = 1;
      this.id = generateId();
      this.offset = 10;

      this.mouse = { x: 0, y: 0 };
      this.mouseUsed = false;

      this.createElement();
      this.setupEventListeners();
      this.updateShader();
    }

    createElement() {
      this.container = document.createElement('div');
      this.container.style.cssText = `
        position: fixed;
        top: 50%;
        left: 50%;
        transform: translate(-50%, -50%);
        width: ${this.width}px;
        height: ${this.height}px;
        overflow: hidden;
        border-radius: 150px;
        box-shadow:
          0 4px 8px rgba(0, 0, 0, 0.25),
          0 -10px 25px inset var(--lens-inner-shadow-1),
          0 -1px 4px 1px inset var(--lens-inner-shadow-2);
        cursor: grab;
        backdrop-filter: url(#${this.id}_filter) blur(0.15px) brightness(1.05) saturate(1.05);
        z-index: 9999;
        pointer-events: auto;
      `;

      this.svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      this.svg.setAttribute('xmlns', 'http://www.w3.org/2000/svg');
      this.svg.setAttribute('width', '0');
      this.svg.setAttribute('height', '0');
      this.svg.style.cssText = `
        position: fixed;
        top: 0;
        left: 0;
        pointer-events: none;
        z-index: 9998;
      `;

      const defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs');
      const filter = document.createElementNS('http://www.w3.org/2000/svg', 'filter');
      filter.setAttribute('id', `${this.id}_filter`);
      filter.setAttribute('filterUnits', 'userSpaceOnUse');
      filter.setAttribute('colorInterpolationFilters', 'sRGB');
      filter.setAttribute('x', '0');
      filter.setAttribute('y', '0');
      filter.setAttribute('width', this.width.toString());
      filter.setAttribute('height', this.height.toString());

      this.feImage = document.createElementNS('http://www.w3.org/2000/svg', 'feImage');
      this.feImage.setAttribute('id', `${this.id}_map`);
      this.feImage.setAttribute('width', this.width.toString());
      this.feImage.setAttribute('height', this.height.toString());

      this.feDisplacementMap = document.createElementNS('http://www.w3.org/2000/svg', 'feDisplacementMap');
      this.feDisplacementMap.setAttribute('in', 'SourceGraphic');
      this.feDisplacementMap.setAttribute('in2', `${this.id}_map`);
      this.feDisplacementMap.setAttribute('xChannelSelector', 'R');
      this.feDisplacementMap.setAttribute('yChannelSelector', 'G');

      filter.appendChild(this.feImage);
      filter.appendChild(this.feDisplacementMap);
      defs.appendChild(filter);
      this.svg.appendChild(defs);

      this.canvas = document.createElement('canvas');
      this.canvas.width = this.width * this.canvasDPI;
      this.canvas.height = this.height * this.canvasDPI;
      this.canvas.style.display = 'none';

      this.context = this.canvas.getContext('2d');
    }

    constrainPosition(x, y) {
      const viewportWidth = window.innerWidth;
      const viewportHeight = window.innerHeight;
      const minX = this.offset;
      const maxX = viewportWidth - this.width - this.offset;
      const minY = this.offset;
      const maxY = viewportHeight - this.height - this.offset;
      return {
        x: Math.max(minX, Math.min(maxX, x)),
        y: Math.max(minY, Math.min(maxY, y))
      };
    }

    setupEventListeners() {
      let isDragging = false;
      let startX, startY, initialX, initialY;

      this.container.addEventListener('mousedown', (e) => {
        isDragging = true;
        this.container.style.cursor = 'grabbing';
        startX = e.clientX;
        startY = e.clientY;
        const rect = this.container.getBoundingClientRect();
        initialX = rect.left;
        initialY = rect.top;
        e.preventDefault();
      });

      document.addEventListener('mousemove', (e) => {
        if (isDragging) {
          const deltaX = e.clientX - startX;
          const deltaY = e.clientY - startY;
          const constrained = this.constrainPosition(initialX + deltaX, initialY + deltaY);
          
          this.container.style.left = constrained.x + 'px';
          this.container.style.top = constrained.y + 'px';
          this.container.style.transform = 'none';
        }

        const rect = this.container.getBoundingClientRect();
        this.mouse.x = (e.clientX - rect.left) / rect.width;
        this.mouse.y = (e.clientY - rect.top) / rect.height;
        
        if (this.mouseUsed) {
          this.updateShader();
        }
      });

      document.addEventListener('mouseup', () => {
        isDragging = false;
        this.container.style.cursor = 'grab';
      });

      window.addEventListener('resize', () => {
        const rect = this.container.getBoundingClientRect();
        const constrained = this.constrainPosition(rect.left, rect.top);
        if (rect.left !== constrained.x || rect.top !== constrained.y) {
          this.container.style.left = constrained.x + 'px';
          this.container.style.top = constrained.y + 'px';
          this.container.style.transform = 'none';
        }
      });
    }

    updateShader() {
      const mouseProxy = new Proxy(this.mouse, {
        get: (target, prop) => {
          this.mouseUsed = true;
          return target[prop];
        }
      });

      this.mouseUsed = false;
      const w = this.width * this.canvasDPI;
      const h = this.height * this.canvasDPI;
      const data = new Uint8ClampedArray(w * h * 4);

      let maxScale = 0;
      const rawValues = [];

      for (let i = 0; i < data.length; i += 4) {
        const x = (i / 4) % w;
        const y = Math.floor(i / 4 / w);
        const pos = this.fragment({ x: x / w, y: y / h }, mouseProxy);
        const dx = pos.x * w - x;
        const dy = pos.y * h - y;
        maxScale = Math.max(maxScale, Math.abs(dx), Math.abs(dy));
        rawValues.push(dx, dy);
      }

      maxScale *= 0.5;

      let index = 0;
      for (let i = 0; i < data.length; i += 4) {
        const r = rawValues[index++] / maxScale + 0.5;
        const g = rawValues[index++] / maxScale + 0.5;
        data[i] = r * 255;
        data[i + 1] = g * 255;
        data[i + 2] = 0;
        data[i + 3] = 255;
      }

      this.context.putImageData(new ImageData(data, w, h), 0, 0);
      this.feImage.setAttributeNS('http://www.w3.org/1999/xlink', 'href', this.canvas.toDataURL());
      this.feDisplacementMap.setAttribute('scale', (maxScale / this.canvasDPI).toString());
    }

    appendTo(parent) {
      if(parent) {
        parent.appendChild(this.svg);
        parent.appendChild(this.container);
      }
    }

    destroy() {
      this.svg.remove();
      this.container.remove();
      this.canvas.remove();
    }
  }


  // ==========================================
  // 2. STATIC UI ELEMENT LIQUID GLASS (NATIVE)
  // ==========================================
  class NativeLiquidGlass {
    constructor(element) {
      this.element = element;
      this.id = generateId();
      
      this.svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      this.svg.style.cssText = 'width:0;height:0;position:absolute;pointer-events:none;';
      
      const defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs');
      const filter = document.createElementNS('http://www.w3.org/2000/svg', 'filter');
      filter.setAttribute('id', `${this.id}_filter`);
      filter.setAttribute('filterUnits', 'userSpaceOnUse');
      filter.setAttribute('colorInterpolationFilters', 'sRGB');
      // Expand filter area to prevent clipping at borders
      filter.setAttribute('x', '-20%');
      filter.setAttribute('y', '-20%');
      filter.setAttribute('width', '140%');
      filter.setAttribute('height', '140%');
      
      this.feImage = document.createElementNS('http://www.w3.org/2000/svg', 'feImage');
      this.feImage.setAttribute('id', `${this.id}_map`);
      
      this.feDisplacementMap = document.createElementNS('http://www.w3.org/2000/svg', 'feDisplacementMap');
      // Critical: Apply to background (backdrop-filter native behavior relies on SourceGraphic representing the backdrop here)
      this.feDisplacementMap.setAttribute('in', 'SourceGraphic'); 
      this.feDisplacementMap.setAttribute('in2', `${this.id}_map`);
      this.feDisplacementMap.setAttribute('xChannelSelector', 'R');
      this.feDisplacementMap.setAttribute('yChannelSelector', 'G');
      
      filter.appendChild(this.feImage);
      filter.appendChild(this.feDisplacementMap);
      defs.appendChild(filter);
      this.svg.appendChild(defs);
      document.body.appendChild(this.svg);

      this.canvas = document.createElement('canvas');
      this.context = this.canvas.getContext('2d');
      
      this.updateBaseSize();

      this.observer = new ResizeObserver(() => this.updateBaseSize());
      this.observer.observe(this.element);
    }
    
    updateBaseSize() {
      const rect = this.element.getBoundingClientRect();
      const w = Math.round(rect.width);
      const h = Math.round(rect.height);
      if (w === 0 || h === 0 || (w === this.width && h === this.height)) return;
      
      this.width = w;
      this.height = h;
      this.canvas.width = w;
      this.canvas.height = h;
      
      this.updateShaderMap();
    }
    
    updateShaderMap() {
      const w = this.width;
      const h = this.height;
      const data = new Uint8ClampedArray(w * h * 4);
      
      const computedStyle = window.getComputedStyle(this.element);
      const borderRadiusStr = computedStyle.borderRadius || '0px';
      let radius = parseFloat(borderRadiusStr) || 16;
      
      let maxScale = 0;
      const rawValues = [];
      
      for (let i = 0; i < data.length; i += 4) {
        const x = (i / 4) % w;
        const y = Math.floor(i / 4 / w);
        
        // Use normalized coordinates for generating the displacement vector
        const ix = x / w - 0.5;
        const iy = y / h - 0.5;
        
        // Exact distance to the rounded rectangle edge in pixel space
        const distanceToEdge = roundedRectSDF(x - w/2, y - h/2, w/2, h/2, radius);
        
        let dx = 0;
        let dy = 0;
        
        // Shuding logic scaled to component:
        // distanceToEdge evaluates to negative inside the rectangle.
        // We want a bump (refraction bevel) near the inner edge.
        // Let's normalize it so we use smoothStep: e.g. edge is 0, inside is negative.
        // We divide by a scalar (e.g. 50px) to make the gradient smooth
        const normalizedD = distanceToEdge / 50; 
        
        // Smoothstep bump formula analogous to Shuding's
        // Here, peak refraction happens near -0.2 (which is 10px inside the edge)
        const displacement = smoothStep(0.2, 0, Math.abs(normalizedD - 0.15));
        const scaled = smoothStep(0, 1, displacement);
        
        // The texture fetch vector in Shuding's
        const fetchX = ix * scaled + 0.5;
        const fetchY = iy * scaled + 0.5;
        
        dx = fetchX * w - x;
        dy = fetchY * h - y;

        maxScale = Math.max(maxScale, Math.abs(dx), Math.abs(dy));
        rawValues.push(dx, dy);
      }
      
      maxScale = maxScale > 0 ? maxScale *= 0.5 : 1; 

      let index = 0;
      for (let i = 0; i < data.length; i += 4) {
        let vx = rawValues[index++];
        let vy = rawValues[index++];
        
        let r, g;
        if (vx === 0 && vy === 0) {
           r = 0.5; g = 0.5;
        } else {
           r = vx / maxScale + 0.5;
           g = vy / maxScale + 0.5;
        }
        
        data[i] = r * 255;
        data[i + 1] = g * 255;
        data[i + 2] = 0;
        data[i + 3] = 255;
      }

      this.context.putImageData(new ImageData(data, w, h), 0, 0);
      
      this.feImage.setAttribute('width', w);
      this.feImage.setAttribute('height', h);
      this.feImage.setAttributeNS('http://www.w3.org/1999/xlink', 'href', this.canvas.toDataURL());
      this.feDisplacementMap.setAttribute('scale', maxScale.toString());

      // Apply the generated filter + native blur to the backdrop-filter!
      // This is crucial: we combine our SVG refraction filter with a native background blur/brightness
      this.element.style.setProperty('backdrop-filter', 
        `url(#${this.id}_filter) blur(0.15px) brightness(1.05) saturate(1.05)`, 'important'
      );
      this.element.style.setProperty('-webkit-backdrop-filter', 
        `url(#${this.id}_filter) blur(0.15px) brightness(1.05) saturate(1.05)`, 'important'
      );
      // Remove the old CSS 'filter' map which warped the UI!
      this.element.style.setProperty('filter', 'none', 'important');
    }

    destroy() {
      if (this.observer) {
        this.observer.disconnect();
        this.observer = null;
      }
      this.svg.remove();
    }
  }

  // --- INIT ---
  document.addEventListener("DOMContentLoaded", () => {
    // Check if the effect is globally enabled via the data attribute on the body or current script context
    const isEnabled = document.body.getAttribute('data-glass-enabled') !== 'false';
    if (!isEnabled) {
      console.log("[LiquidGlass] Disabled by admin setting.");
      return;
    }

    // 1. Create the Shuding draggable lens
    const shader = new Shader({
      width: 200,
      height: 150,
      fragment: (uv, mouse) => {
        const ix = uv.x - 0.5;
        const iy = uv.y - 0.5;
        const distanceToEdge = roundedRectSDF(
          ix,
          iy,
          0.3,
          0.2,
          0.6
        );
        const displacement = smoothStep(0.8, 0, distanceToEdge - 0.15);
        const scaled = smoothStep(0, 1, displacement);
        return texture(ix * scaled + 0.5, iy * scaled + 0.5);
      }
    });
    shader.appendTo(document.body);

    // 2. Attach tailored optical liquid glass to all native UI components matching .liquid-glass
    const nativeInstances = [];
    document.querySelectorAll('.liquid-glass').forEach(el => {
      nativeInstances.push(new NativeLiquidGlass(el));
    });

    // Expose global handle with unified destroy
    window.liquidGlass = {
      shader,
      destroy() {
        shader.destroy();
        nativeInstances.forEach(inst => inst.destroy());
        nativeInstances.length = 0;
      }
    };
  });

})();
