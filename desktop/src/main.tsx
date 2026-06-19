import React, { Component, type ErrorInfo, type ReactNode } from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import "./styles.css";

class AppErrorBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {};

  static getDerivedStateFromError(error: Error) { return { error }; }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Asayn desktop render failure", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return <div className="fatal-error"><div className="boot-mark" aria-label="Asayn"/><h1>Asayn could not render the application.</h1><pre>{this.state.error.message}</pre><p>Restart the application. If the problem persists, include this message in a bug report.</p></div>;
    }
    return this.props.children;
  }
}

ReactDOM.createRoot(document.getElementById("root")!).render(<React.StrictMode><AppErrorBoundary><App /></AppErrorBoundary></React.StrictMode>);
