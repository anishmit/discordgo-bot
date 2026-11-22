-- The bot's database schema, for reference. To regenerate it, run this on the VM from the repo root:
--   sed -i '4,$d' internal/database/schema.sql && pg_dump --schema-only --no-owner --no-privileges "$(grep '^DATABASE_URL=' .env | cut -d= -f2-)" | sed '/^\\\(un\)\?restrict /d' >> internal/database/schema.sql

--
-- PostgreSQL database dump
--


-- Dumped from database version 17.12 (Debian 17.12-0+deb13u1)
-- Dumped by pg_dump version 17.12 (Debian 17.12-0+deb13u1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: pg_trgm; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;


--
-- Name: EXTENSION pg_trgm; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION pg_trgm IS 'text similarity measurement and index searching based on trigrams';


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: first_messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.first_messages (
    iso_date date NOT NULL,
    content text NOT NULL,
    timestamp_ms bigint NOT NULL,
    message_id bigint NOT NULL,
    timezone character varying(100) NOT NULL,
    user_id bigint NOT NULL,
    speed bigint
);


--
-- Name: first_send_bias; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.first_send_bias (
    iso_date date NOT NULL,
    sender text NOT NULL,
    bias_us bigint NOT NULL
);


--
-- Name: gemini_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.gemini_settings (
    channel_id bigint NOT NULL,
    user_id bigint NOT NULL,
    settings jsonb NOT NULL
);


--
-- Name: latex_problems; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.latex_problems (
    id bigint NOT NULL,
    title text NOT NULL,
    solution text NOT NULL,
    best_solution text NOT NULL
);


--
-- Name: latex_problems_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.latex_problems ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.latex_problems_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: latex_solutions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.latex_solutions (
    id bigint NOT NULL,
    problem_id bigint NOT NULL,
    user_id bigint NOT NULL,
    timestamp_ms bigint NOT NULL,
    time_taken double precision NOT NULL,
    solution text NOT NULL
);


--
-- Name: latex_solutions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.latex_solutions ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.latex_solutions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: message_logs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.message_logs (
    id bigint NOT NULL,
    channel_id bigint NOT NULL,
    message_id bigint NOT NULL,
    user_id bigint NOT NULL,
    timestamp_ms bigint NOT NULL,
    old_content text NOT NULL,
    new_content text
);


--
-- Name: message_logs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.message_logs ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.message_logs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.messages (
    message_id bigint NOT NULL,
    user_id bigint NOT NULL,
    timestamp_ms bigint NOT NULL,
    content text
);


--
-- Name: tracked_messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tracked_messages (
    message_id bigint NOT NULL,
    channel_id bigint NOT NULL,
    user_id bigint NOT NULL,
    content text NOT NULL
);


--
-- Name: first_messages first_messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.first_messages
    ADD CONSTRAINT first_messages_pkey PRIMARY KEY (iso_date);


--
-- Name: first_send_bias first_send_bias_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.first_send_bias
    ADD CONSTRAINT first_send_bias_pkey PRIMARY KEY (iso_date, sender);


--
-- Name: gemini_settings gemini_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gemini_settings
    ADD CONSTRAINT gemini_settings_pkey PRIMARY KEY (channel_id, user_id);


--
-- Name: latex_problems latex_problems_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.latex_problems
    ADD CONSTRAINT latex_problems_pkey PRIMARY KEY (id);


--
-- Name: latex_solutions latex_solutions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.latex_solutions
    ADD CONSTRAINT latex_solutions_pkey PRIMARY KEY (id);


--
-- Name: message_logs message_logs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.message_logs
    ADD CONSTRAINT message_logs_pkey PRIMARY KEY (id);


--
-- Name: messages messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.messages
    ADD CONSTRAINT messages_pkey PRIMARY KEY (message_id);


--
-- Name: tracked_messages tracked_messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tracked_messages
    ADD CONSTRAINT tracked_messages_pkey PRIMARY KEY (message_id);


--
-- Name: latex_solutions_problem_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX latex_solutions_problem_id_idx ON public.latex_solutions USING btree (problem_id);


--
-- Name: message_logs_channel_id_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX message_logs_channel_id_id_idx ON public.message_logs USING btree (channel_id, id);


--
-- Name: messages_content_trgm_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX messages_content_trgm_idx ON public.messages USING gin (content public.gin_trgm_ops);


--
-- Name: messages_timestamp_ms_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX messages_timestamp_ms_idx ON public.messages USING btree (timestamp_ms);


--
-- Name: messages_user_id_timestamp_ms_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX messages_user_id_timestamp_ms_idx ON public.messages USING btree (user_id, timestamp_ms);


--
-- Name: latex_solutions latex_solutions_problem_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.latex_solutions
    ADD CONSTRAINT latex_solutions_problem_id_fkey FOREIGN KEY (problem_id) REFERENCES public.latex_problems(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--


