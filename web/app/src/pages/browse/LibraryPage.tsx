import { LibraryBrowser } from "../LibraryBrowser";
import { useScrollRestoration } from "../../lib/appShared";
import { ItemGrid } from "../../components/media/MediaCards";

export function LibraryPage() {
  useScrollRestoration("libraries");
  return <LibraryBrowser renderItems={(items) => <ItemGrid items={items} />} />;
}

