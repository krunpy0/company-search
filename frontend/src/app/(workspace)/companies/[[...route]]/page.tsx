import { ProtectedWorkspace, workspaceViewport } from "../../[...route]/protected-workspace";

export const viewport = workspaceViewport;

export default function CompaniesPage(props: PageProps<"/companies/[[...route]]">) {
  return <ProtectedWorkspace section="companies" {...props} />;
}
