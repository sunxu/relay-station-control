import { defineConfig } from "orval";

export default defineConfig({
  control: {
    input: "../api/openapi.yaml",
    output: {
      target: "./src/api/generated/control.ts",
      client: "react-query",
      httpClient: "fetch",
      clean: true,
    },
  },
});
