// vite.config.js
import { sveltekit } from "file:///Users/kw/Documents/Code/ToDoApp/web/node_modules/@sveltejs/kit/src/exports/vite/index.js";
var vite_config_default = {
  plugins: [sveltekit()],
  server: {
    port: 5173,
    // In development the app runs on 5173 and the API on 8080. Proxying rather
    // than pointing fetch at another origin keeps the session cookie
    // first-party, which is what production looks like — otherwise every
    // cookie and CSRF behaviour would differ between dev and the real thing.
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: false,
        ws: true
      }
    }
  }
};
export {
  vite_config_default as default
};
//# sourceMappingURL=data:application/json;base64,ewogICJ2ZXJzaW9uIjogMywKICAic291cmNlcyI6IFsidml0ZS5jb25maWcuanMiXSwKICAic291cmNlc0NvbnRlbnQiOiBbImNvbnN0IF9fdml0ZV9pbmplY3RlZF9vcmlnaW5hbF9kaXJuYW1lID0gXCIvVXNlcnMva3cvRG9jdW1lbnRzL0NvZGUvVG9Eb0FwcC93ZWJcIjtjb25zdCBfX3ZpdGVfaW5qZWN0ZWRfb3JpZ2luYWxfZmlsZW5hbWUgPSBcIi9Vc2Vycy9rdy9Eb2N1bWVudHMvQ29kZS9Ub0RvQXBwL3dlYi92aXRlLmNvbmZpZy5qc1wiO2NvbnN0IF9fdml0ZV9pbmplY3RlZF9vcmlnaW5hbF9pbXBvcnRfbWV0YV91cmwgPSBcImZpbGU6Ly8vVXNlcnMva3cvRG9jdW1lbnRzL0NvZGUvVG9Eb0FwcC93ZWIvdml0ZS5jb25maWcuanNcIjtpbXBvcnQgeyBzdmVsdGVraXQgfSBmcm9tICdAc3ZlbHRlanMva2l0L3ZpdGUnO1xuXG5leHBvcnQgZGVmYXVsdCB7XG5cdHBsdWdpbnM6IFtzdmVsdGVraXQoKV0sXG5cdHNlcnZlcjoge1xuXHRcdHBvcnQ6IDUxNzMsXG5cdFx0Ly8gSW4gZGV2ZWxvcG1lbnQgdGhlIGFwcCBydW5zIG9uIDUxNzMgYW5kIHRoZSBBUEkgb24gODA4MC4gUHJveHlpbmcgcmF0aGVyXG5cdFx0Ly8gdGhhbiBwb2ludGluZyBmZXRjaCBhdCBhbm90aGVyIG9yaWdpbiBrZWVwcyB0aGUgc2Vzc2lvbiBjb29raWVcblx0XHQvLyBmaXJzdC1wYXJ0eSwgd2hpY2ggaXMgd2hhdCBwcm9kdWN0aW9uIGxvb2tzIGxpa2UgXHUyMDE0IG90aGVyd2lzZSBldmVyeVxuXHRcdC8vIGNvb2tpZSBhbmQgQ1NSRiBiZWhhdmlvdXIgd291bGQgZGlmZmVyIGJldHdlZW4gZGV2IGFuZCB0aGUgcmVhbCB0aGluZy5cblx0XHRwcm94eToge1xuXHRcdFx0Jy9hcGknOiB7XG5cdFx0XHRcdHRhcmdldDogJ2h0dHA6Ly9sb2NhbGhvc3Q6ODA4MCcsXG5cdFx0XHRcdGNoYW5nZU9yaWdpbjogZmFsc2UsXG5cdFx0XHRcdHdzOiB0cnVlXG5cdFx0XHR9XG5cdFx0fVxuXHR9XG59O1xuIl0sCiAgIm1hcHBpbmdzIjogIjtBQUE4UixTQUFTLGlCQUFpQjtBQUV4VCxJQUFPLHNCQUFRO0FBQUEsRUFDZCxTQUFTLENBQUMsVUFBVSxDQUFDO0FBQUEsRUFDckIsUUFBUTtBQUFBLElBQ1AsTUFBTTtBQUFBO0FBQUE7QUFBQTtBQUFBO0FBQUEsSUFLTixPQUFPO0FBQUEsTUFDTixRQUFRO0FBQUEsUUFDUCxRQUFRO0FBQUEsUUFDUixjQUFjO0FBQUEsUUFDZCxJQUFJO0FBQUEsTUFDTDtBQUFBLElBQ0Q7QUFBQSxFQUNEO0FBQ0Q7IiwKICAibmFtZXMiOiBbXQp9Cg==
