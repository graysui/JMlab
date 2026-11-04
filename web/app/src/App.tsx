import { ErrorBoundary } from "./components/ErrorBoundary";
import { Layout } from "./layouts/ApplicationLayout";

export default function App() {
  return (
    <ErrorBoundary>
      <Layout />
    </ErrorBoundary>
  );
}
