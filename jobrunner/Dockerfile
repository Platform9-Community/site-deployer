FROM python:3.11.4-bullseye

WORKDIR /deployer

RUN sed -i 's/deb.debian.org\/debian /mirror.cogentco.com\/debian /g' /etc/apt/sources.list
RUN apt update && apt install lsb-release -y
RUN wget -qO - 'https://proget.makedeb.org/debian-feeds/prebuilt-mpr.pub' | gpg --dearmor | tee /usr/share/keyrings/prebuilt-mpr-archive-keyring.gpg 1> /dev/null
RUN echo "deb [arch=amd64 signed-by=/usr/share/keyrings/prebuilt-mpr-archive-keyring.gpg] https://proget.makedeb.org prebuilt-mpr $(lsb_release -cs)" | tee /etc/apt/sources.list.d/prebuilt-mpr.list
RUN apt update && apt install vim just gettext-base jq -y
COPY requirements.txt ./
COPY requirements.yml ./
RUN python -m pip install --no-cache-dir -r requirements.txt
RUN ansible-galaxy install -r requirements.yml
RUN rm -f requirements.txt requirements.yml
RUN rm -f /usr/bin/python3 && ln -s /usr/local/bin/python /usr/bin/python3
COPY pmk_cluster.yaml.tmpl ./
COPY justfile ./
COPY create_cluster.sh ./

CMD [ "python", "-c" "print('I am site deployer')" ]
