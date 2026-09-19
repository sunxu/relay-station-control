import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "antd/dist/reset.css";
import App from "./App";
import { FrontendFoundationProvider } from "./foundation/FrontendFoundationProvider";
import "./styles.css";

const queryClient = new QueryClient();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <FrontendFoundationProvider>
        <App />
      </FrontendFoundationProvider>
    </QueryClientProvider>
  </React.StrictMode>,
);
