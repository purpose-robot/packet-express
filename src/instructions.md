bun init -y
bun add cytoscape cytoscape-fcose
bun build --compile --target=browser --minify ./index.html --outdir=dis
