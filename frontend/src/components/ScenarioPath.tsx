import { Fragment, memo } from "react";
import { AlertTriangle, ArrowRight } from "lucide-react";
import type { Node } from "@xyflow/react";

import type { FlowNodeData } from "../flowTypes";
import type { ScenarioGraphValidation } from "../graphCompiler";

export const ScenarioPath = memo(function ScenarioPath({
  nodes,
  validation,
}: {
  nodes: Node<FlowNodeData>[];
  validation: ScenarioGraphValidation;
}) {
  const nodesById = new Map(nodes.map((node) => [node.id, node]));
  return (
    <section
      className={`scenario-path${validation.compiled ? "" : " scenario-path-invalid"}`}
      aria-label="Scenario execution path"
    >
      <div className="scenario-path-heading">
        <span>Execution path</span>
        <small>{validation.compiled ? `${validation.compiled.path.length} nodes` : "Invalid graph"}</small>
      </div>
      {validation.compiled ? (
        <div className="scenario-path-steps">
          {validation.compiled.path.map((id, index) => {
            const node = nodesById.get(id);
            return (
              <Fragment key={id}>
                {index > 0 ? <ArrowRight className="scenario-path-arrow" size={14} aria-hidden="true" /> : null}
                <div className="scenario-path-step" title={`${node?.data.kind ?? "node"}: ${id}`}>
                  <b>{index + 1}</b>
                  <span>{node?.data.label ?? id}</span>
                  <small>{id}</small>
                </div>
              </Fragment>
            );
          })}
        </div>
      ) : (
        <div className="scenario-path-error" role="alert">
          <AlertTriangle size={16} aria-hidden="true" />
          <span>{validation.issues[0]?.message ?? "Scenario graph is invalid."}</span>
          {validation.issues.length > 1 ? <small>+{validation.issues.length - 1} more</small> : null}
        </div>
      )}
    </section>
  );
});
