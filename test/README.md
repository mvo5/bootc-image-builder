Integration tests for osbuild-deploy-container
----------------------------------------------

This directory contans integration tests for osbuild-deploy-container.
They can be run in two ways:
1. On the local machine by just running `pytest`
2. Via `tmt` [0] which will spin up a clean VM and run the tests inside: 

	tmt run -vvv




[0] https://github.com/teemtee/tmt
