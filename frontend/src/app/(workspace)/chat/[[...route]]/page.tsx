import { ProtectedWorkspace, workspaceViewport } from "../../[...route]/protected-workspace";

export const viewport = workspaceViewport;

export default function ChatPage(props: PageProps<"/chat/[[...route]]">) {
  return <ProtectedWorkspace section="chat" {...props} />;
}
